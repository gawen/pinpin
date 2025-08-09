package pinpin

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"os"
	"sync"
	"time"

	"github.com/schollz/progressbar/v3"
)

func DialTimeout(address string, timeout time.Duration) (*Conn, error) {
	log := slog.Default()
	log.Debug("connecting", "address", address)
	conn, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		return nil, err
	}

	log.Debug("connected")
	return &Conn{
		conn: conn,
		log:  log,
	}, nil
}

type Conn struct {
	conn net.Conn
	mu   sync.Mutex
	log  *slog.Logger
}

func (c *Conn) Close() error {
	return c.conn.Close()
}

func (c *Conn) writedMsg(msg []byte) error {
	if len(msg) < 1 {
		panic("expected message with at least a command, got none")
	} else if len(msg) > math.MaxUint8 {
		panic("expected message length to be encodable in a byte")
	}

	buf := make([]byte, 1+len(msg)+4)
	buf[0] = byte(len(msg) + 4)
	copy(buf[1:], msg)
	binary.LittleEndian.PutUint32(buf[1+len(msg):], Crc32(msg))

	c.log.Debug("send", "buf", hex.EncodeToString(buf))

	//c.conn.SetDeadline(time.Now().Add(10 * time.Second))
	if sz, err := c.conn.Write(buf); err != nil {
		return fmt.Errorf("unable to write: %w", err)
	} else if sz != len(buf) {
		return fmt.Errorf("unable to write %dB", len(buf))
	}

	return nil
}

func (c *Conn) readMsg() ([]byte, error) {
	// read length of message
	hdr := make([]byte, 1)
	sz, err := c.conn.Read(hdr)
	if err != nil || sz != 1 {
		return nil, fmt.Errorf("unable to read length header: %w", err)
	}

	// read message + crc
	payloadLen := int(hdr[0])
	payload := make([]byte, payloadLen)
	_, err = io.ReadAtLeast(c.conn, payload, payloadLen)
	if err != nil {
		return nil, fmt.Errorf("unable to read payload of %dB: %w", payloadLen, err)
	}
	c.log.Debug("recv", "buf", hex.EncodeToString(payload))
	msg := payload[:payloadLen-4]
	gotDigest := binary.LittleEndian.Uint32(payload[payloadLen-4:])

	// check crc
	if expectedDigest := Crc32(msg); expectedDigest != gotDigest {
		return nil, fmt.Errorf("wrong crc32: expected %.8x, got %.8x", expectedDigest, gotDigest)
	}

	return msg, nil
}

func (c *Conn) call(inp []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.writedMsg(inp); err != nil {
		return nil, err
	}

	return c.readMsg()
}

func (c *Conn) Ping() error {
	_, err := c.call([]byte{CommandPing})
	return err
}

func (c *Conn) GetSDSize() (uint32, error) {
	out, err := c.call([]byte{CommandGetSDSize})
	if err != nil {
		return 0, err
	}

	return binary.LittleEndian.Uint32(out[1:5]), nil
}

func (c *Conn) EndSynchronization() error {
	_, err := c.call([]byte{CommandEndSynchronization})
	return err
}

func (c *Conn) GetNumberOfFiles() (uint16, error) {
	out, err := c.call([]byte{CommandGetNumberOfFiles})
	if err != nil {
		return 0, err
	}

	return binary.LittleEndian.Uint16(out[1:3]), nil
}

type FileInformation struct {
	Path   string
	Size   uint32
	Sha256 []byte
}

func (c *Conn) GetFileInformation(idx uint16, computeSha256 bool) (*FileInformation, error) {
	inp := make([]byte, 1+2+1)
	inp[0] = CommandGetFileInformation
	binary.LittleEndian.PutUint16(inp[1:], idx)
	if computeSha256 {
		inp[3] = 1
	}

	out, err := c.call(inp)
	if err != nil {
		return nil, err
	}

	switch out[1] {
	case 0x00:
		// ok
	case 0x02:
		return nil, fmt.Errorf("bad file index")
	case 0x03:
		return nil, os.ErrExist
	case 0x04:
		return nil, fmt.Errorf("unable to open file")
	default:
		panic(fmt.Sprintf("unexpected `CommandGetFileInformation` status: %d", out[1]))
	}

	pathLen := int(out[2])
	path := string(out[3 : 3+pathLen])
	size := binary.LittleEndian.Uint32(out[3+pathLen : 3+pathLen+4])
	sha256 := out[3+pathLen+4 : 3+pathLen+4+sha256.Size]

	return &FileInformation{
		Path:   path,
		Size:   size,
		Sha256: sha256,
	}, nil
}

func (c *Conn) GetFile(path string, w io.Writer) error {
	pathRaw := []byte(path)
	inp := make([]byte, 1+len(pathRaw))
	inp[0] = CommandGetFile
	copy(inp[1:], pathRaw)

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.writedMsg(inp); err != nil {
		return err
	}

	out, err := c.readMsg()
	if err != nil {
		return err
	}

	if status := out[1]; status != 0x00 {
		return fmt.Errorf("unable to get file '%s'", path)
	}

	filePathLen := int(out[2])
	//retFilePath := out[3 : 3+pathLen]
	fileSize := binary.LittleEndian.Uint32(out[3+filePathLen : 3+filePathLen+4])
	connFileReader := io.LimitReader(c.conn, int64(fileSize))

	bar := progressbar.DefaultBytes(int64(fileSize), "reading "+path)
	defer bar.Close()
	barReader := progressbar.NewReader(connFileReader, bar)

	gotFileSize, err := io.Copy(w, &barReader)
	if err != nil {
		return fmt.Errorf("unable to read '%s': %w", path, err)
	} else if gotFileSize != int64(fileSize) {
		return fmt.Errorf("expected %dB, got %dB for '%s'", fileSize, gotFileSize, path)
	}

	return nil
}

func (c *Conn) UploadFileReader(
	fileName string,
	reader io.Reader,
	size uint32,
	checksum []byte,
) error {
	if len(checksum) != sha256.Size {
		panic("checksum should be a SHA256")
	}

	// compute sha256
	inp := make([]byte, 1+1+len(fileName)+4+sha256.Size)
	inp[0] = CommandUploadFile
	inp[1] = byte(len(fileName))
	copy(inp[2:2+len(fileName)], []byte(fileName))
	binary.LittleEndian.PutUint32(inp[2+len(fileName):2+len(fileName)+4], size)
	copy(inp[2+len(fileName)+4:], checksum[:])

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.writedMsg(inp); err != nil {
		return err
	}

	out, err := c.readMsg()
	if err != nil {
		return err
	}

	switch out[1] {
	case 0x00:
		// ok
	case 0x02:
		return fmt.Errorf("not enough space")
	case 0x03:
		return fmt.Errorf("filename too large")
	case 0x05:
		panic("bad command length")
	case 0x07:
		return fmt.Errorf("fail open upload file")
	default:
		panic(fmt.Sprintf("unexpected `CommandUploadFile` status: %d", out[1]))
	}

	bar := progressbar.DefaultBytes(int64(size), "writing "+fileName)
	defer bar.Close()
	barReader := progressbar.NewReader(reader, bar)

	if sz, err := io.Copy(c.conn, &barReader); err != nil {
		return fmt.Errorf("unable to write file '%s': %w", fileName, err)
	} else if sz < int64(size) {
		return io.ErrShortWrite
	}

	out2, err := c.readMsg()
	if err != nil {
		return err
	}

	switch out2[1] {
	case 0x01:
		// sha valid
	case 0x04:
		return fmt.Errorf("sha invalid")
	default:
		panic(fmt.Sprintf("unexpected `CommandUploadFile` end status: %d", out2[1]))
	}

	return nil
}

func (c *Conn) UploadBytes(
	fileName string,
	raw []byte,
) error {
	if len(raw) >= math.MaxUint32 {
		return fmt.Errorf("file too large")
	}

	checksum := sha256.Sum256(raw)

	return c.UploadFileReader(fileName, bytes.NewReader(raw), uint32(len(raw)), checksum[:])
}

func (c *Conn) UploadReadSeeker(
	fileName string,
	fh io.ReadSeeker,
) error {
	if _, err := fh.Seek(0, io.SeekStart); err != nil {
		return err
	}

	// compute sha256
	hasher := sha256.New()
	size, err := io.Copy(hasher, fh)
	if err != nil {
		return err
	}

	if size >= math.MaxUint32 {
		return fmt.Errorf("file too large")
	}

	if _, err := fh.Seek(0, io.SeekStart); err != nil {
		return err
	}

	return c.UploadFileReader(fileName, fh, uint32(size), hasher.Sum(nil))
}

func (c *Conn) UploadLocaFile(
	fileName string,
	localFilePath string,
) error {
	fh, err := os.Open(localFilePath)
	if err != nil {
		return err
	}
	defer fh.Close()

	return c.UploadReadSeeker(fileName, fh)
}

func (c *Conn) UpdatePlaylist(path string) error {
	inp := make([]byte, 1+len(path))
	inp[0] = CommandUpdatePlaylist
	copy(inp[1:], []byte(path))
	out, err := c.call(inp)
	if err != nil {
		return err
	}

	switch out[1] {
	case 0x00:
		// ok?
	case 0x01:
		return fmt.Errorf("invalid minimum length of filename")
	case 0x04:
		return fmt.Errorf("failed to minifier the JSON file")
	case 0x05:
		return fmt.Errorf("failed open JSON file")
	case 0x06:
		return fmt.Errorf("failed to open playlist binary file")
	case 0x07:
		return fmt.Errorf("JSON root element is not an array")
	case 0x08:
		return fmt.Errorf("min root category size is invalid")
	case 0x0f:
		return fmt.Errorf("failed to rename binary tmp file")
	case 0x10:
		return fmt.Errorf("invalid type found in binary file")
	case 0x11:
		return fmt.Errorf("category JPEG not found")
	case 0x12:
		return fmt.Errorf("music mp32 not found")
	case 0x13:
		return fmt.Errorf("favorite not found")
	case 0x14:
		return fmt.Errorf("too many favorite")
	case 0x15:
		return fmt.Errorf("failed to create favorite file")
	case 0x16:
		return fmt.Errorf("failed to read lst fav file")
	case 0x17:
		return fmt.Errorf("unable to read lst fav file")
	case 0x18:
		return fmt.Errorf("failed to open sdcard dir")
	default:
		panic(fmt.Sprintf("unexpected `CommandUpdatePlaylist` end status: %d", out[1]))
	}

	return nil
}
