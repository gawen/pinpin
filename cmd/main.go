package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gawen/pinpin"
	"github.com/google/uuid"
	"github.com/schollz/progressbar/v3"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s <path to library to upload>\n", os.Args[0])
	}

	flag.Parse()
	libraryPath := flag.Arg(0)

	cachePath := filepath.Join(libraryPath, ".cache")
	_ = os.Mkdir(cachePath, 0755)
	libraryNodes, err := readLibrary(libraryPath, cachePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "unable to read library to upload: %s\n", err.Error())
		return
	}

	fmt.Fprintf(os.Stderr, "🎧 Library read!\n")
	for firstIdx, firstNode := range libraryNodes {
		fmt.Fprintf(os.Stderr, "%d. %s\n", firstIdx+1, firstNode.Title)
		for secondIdx, secondNode := range firstNode.Children {
			fmt.Fprintf(os.Stderr, "  %d. %s\n", secondIdx+1, secondNode.Title)
		}
	}
	fmt.Fprintf(os.Stderr, "\n")

	fmt.Fprintf(os.Stderr, "🛜 connecting to the Merlin...\n")
	fmt.Fprintf(os.Stderr, "ℹ️ set your Merlin in mode 'TRANSFERT', search for a Wi-Fi network named 'MERLIN_' and connect to it with password 'MERLIN_APP'.\n")
	var conn *pinpin.Conn
	for range 10 {
		var err error
		conn, err = pinpin.DialTimeout("192.168.4.1:50000", 5*time.Second)
		if err != nil {
			fmt.Fprintf(os.Stderr, "unable to connect: %s\n", err.Error())
			time.Sleep(time.Second)
			continue
		}

		if err := conn.Ping(); err != nil {
			conn.Close()
			fmt.Fprintf(os.Stderr, "unable to ping: %s\n", err.Error())
			time.Sleep(time.Second)
			continue
		}

		break
	}
	if conn == nil {
		return
	}
	defer conn.Close()

	fmt.Fprintf(os.Stderr, "🛜 connected ✅\n")

	// list already existing files
	fileCount, err := conn.GetNumberOfFiles()
	if err != nil {
		fmt.Fprintf(os.Stderr, "unable to get file count in Merlin: %s", err.Error())
		os.Exit(-1)
	}

	existingFileSize := make(map[string]uint32)
	listProg := progressbar.Default(int64(fileCount), "listing files...")
	for idx := range fileCount {
		fi, err := conn.GetFileInformation(idx, false)
		if err != nil {
			fmt.Fprintf(os.Stderr, "unable to get file #%d's information in Merlin: %s", idx, err.Error())
			os.Exit(-1)
		}

		existingFileSize[fi.Path] = fi.Size
		listProg.Add(1)
	}
	listProg.Close()

	// list & transfer missing files
	var transferFiles []string
	for _, firstNode := range libraryNodes {
		transferFiles = append(transferFiles,
			firstNode.UUID+".jpg",
		)
		for _, secondNode := range firstNode.Children {
			transferFiles = append(transferFiles,
				secondNode.UUID+".mp3",
				secondNode.UUID+".jpg",
			)
		}
	}

	for _, remoteFilePath := range transferFiles {
		localFilePath := filepath.Join(cachePath, remoteFilePath)
		fi, err := os.Stat(localFilePath)
		if err != nil {
			panic(err)
		}

		if size, has := existingFileSize[remoteFilePath]; has && size == uint32(fi.Size()) {
			continue
		}

		if err := conn.UploadLocaFile(remoteFilePath, localFilePath); err != nil {
			fmt.Fprintf(os.Stderr, "unable to transfer file '%s': %s", remoteFilePath, err.Error())
			os.Exit(-1)
		}
	}

	// get playlist
	var playlistBin bytes.Buffer
	if err := conn.GetFile("playlist.bin", &playlistBin); err != nil {
		fmt.Fprintf(os.Stderr, "unable to get 'playlist.bin': %s\n", err.Error())
		os.Exit(-1)
	}

	playlistItems, err := pinpin.DecodePlaylistBin(playlistBin.Bytes())

	if err != nil {
		fmt.Fprintf(os.Stderr, "unable to decode 'playlist.bin': %s\n", err.Error())
		os.Exit(-1)
	}

	oldTree, err := pinpin.BuildPlaylistTree(playlistItems)
	if err != nil {
		fmt.Fprintf(os.Stderr, "unable to process `playlist.bin`: %s\n", err.Error())
		os.Exit(-1)
	}

	// replace old pinpin nodes new ones
	var newTree []*pinpin.PlaylistTreeNode
	for _, node := range oldTree {
		u, err := uuid.Parse(node.UUID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "unable to parse UUID '%s'. ignore\n", node.UUID)
			newTree = append(newTree, node)
			continue
		}

		// remove node if it is from pinpin
		if !isPinpinUUID(u) {
			newTree = append(newTree, node)
		}
	}

	newTree = append(newTree, libraryNodes...)

	playlistJsonRaw, err := pinpin.MarshalPlaylistJson(newTree)

	if err != nil {
		fmt.Fprintf(os.Stderr, "unable to generate `playlist.json`: %s\n", err.Error())
		os.Exit(-1)
	}

	if err := conn.UploadBytes("playlist.json", playlistJsonRaw); err != nil {
		fmt.Fprintf(os.Stderr, "unable to upload `playlist.json`: %s\n", err.Error())
		os.Exit(-1)
	}

	if err := conn.UpdatePlaylist("playlist.json"); err != nil {
		fmt.Fprintf(os.Stderr, "unable to apply `playlist.json`: %s\n", err.Error())
		os.Exit(-1)
	}

	if err := conn.EndSynchronization(); err != nil {
		panic(err)
	}

	fmt.Fprintf(os.Stderr, "✅ transfered!\n")
}

func readLibrary(basePath string, cachePath string) ([]*pinpin.PlaylistTreeNode, error) {

	firstEntries, err := os.ReadDir(basePath)
	if err != nil {
		return nil, err
	}

	var nodes []*pinpin.PlaylistTreeNode
	for _, firstEntry := range firstEntries {
		firstName := firstEntry.Name()
		if strings.HasPrefix(firstName, ".") {
			continue
		}
		firstPath := filepath.Join(basePath, firstName)

		firstEntryInfo, err := firstEntry.Info()
		if err != nil {
			fmt.Fprintf(os.Stderr, "unable to stat file '%s': %s\n", firstPath, err.Error())
			continue
		}

		if !firstEntry.IsDir() {
			fmt.Fprintf(os.Stderr, "unexpected regular file '%s'\n", firstPath)
			continue
		}

		secondEntries, err := os.ReadDir(firstPath)
		if err != nil {
			return nil, err
		}

		firstTitle, _ := strings.CutSuffix(firstName, filepath.Ext(firstName))
		if err := checkTitle(firstTitle); err != nil {
			fmt.Fprintf(os.Stderr, "invalid title '%s': %s\n", firstName, err.Error())
			continue
		}

		firstUUID := titlePinpinUUID(firstName)
		firstNode := new(pinpin.PlaylistTreeNode)
		firstNode.UUID = firstUUID.String()
		firstNode.Title = firstTitle
		firstNode.AddTimeUnix = uint32(firstEntryInfo.ModTime().Unix())

		// pick an image
		// TODO allow to pick any image
		if fh, err := os.Create(filepath.Join(cachePath, firstUUID.String()+".jpg")); err != nil {
			fmt.Fprintf(os.Stderr, "unable to write image for '%s': %s\n", firstPath, err.Error())
			continue
		} else {
			fh.Write(pickAssetJpegRaw(firstUUID[:]))
			fh.Close()
		}

		for _, secondEntry := range secondEntries {
			secondName := secondEntry.Name()
			if strings.HasPrefix(secondName, ".") {
				continue
			}
			secondPath := filepath.Join(firstPath, secondName)

			secondEntryInfo, err := secondEntry.Info()
			if err != nil {
				fmt.Fprintf(os.Stderr, "unable to stat file '%s': %s\n", secondPath, err.Error())
				continue
			}

			if secondEntry.IsDir() {
				fmt.Fprintf(os.Stderr, "unexpected directory file '%s'\n", secondPath)
				continue
			}

			secondTitle, _ := strings.CutSuffix(secondName, filepath.Ext(secondName))
			if err := checkTitle(secondTitle); err != nil {
				fmt.Fprintf(os.Stderr, "invalid title '%s': %s\n", secondName, err.Error())
				continue
			}

			switch strings.ToLower(filepath.Ext(secondName)) {
			case ".mp3", ".mp4", ".webm", ".m4a", ".wav", ".opus":
				// derive UUID
				secondUUID, err := filePinpinUUID(secondPath)
				if err != nil {
					fmt.Fprintf(os.Stderr, "unable to derive UUID from '%s': %s\n", secondPath, err.Error())
					continue
				}

				// transcode
				secondTranscodedPath := filepath.Join(cachePath, secondUUID.String()+".mp3")
				if _, err := os.Stat(secondTranscodedPath); err != nil {
					_ = os.Remove(secondTranscodedPath)
					fmt.Fprintf(os.Stderr, "transcoding '%s'...\n", secondPath)
					if err := runFfmpeg("-i", secondPath,
						"-map", "0:a:0",
						"-c:a", "libmp3lame",
						"-b:a", "128k",
						"-ar", "44100",
						"-ac", "2",
						"-sample_fmt", "fltp",
						secondTranscodedPath,
					); err != nil {
						fmt.Fprintf(os.Stderr, "unable to transcode '%s': %s\n", secondPath, err.Error())
						continue
					}
				}

				// pick an image
				// TODO allow to pick any image
				if fh, err := os.Create(filepath.Join(cachePath, secondUUID.String()+".jpg")); err != nil {
					fmt.Fprintf(os.Stderr, "unable to write image for '%s': %s\n", secondPath, err.Error())
					continue
				} else {
					fh.Write(pickAssetJpegRaw(secondUUID[:]))
					fh.Close()
				}

				secondNode := new(pinpin.PlaylistTreeNode)
				secondNode.UUID = secondUUID.String()
				secondNode.Title = secondTitle
				secondNode.AddTimeUnix = uint32(secondEntryInfo.ModTime().Unix())

				firstNode.Children = append(firstNode.Children, secondNode)

			default:
				fmt.Fprintf(os.Stderr, "unexpected file '%s'. ignore.\n", secondPath)
			}
		}
		if len(firstNode.Children) > 0 {
			sort.Slice(firstNode.Children, func(i, j int) bool {
				return firstNode.Children[i].AddTimeUnix > firstNode.Children[j].AddTimeUnix
			})

			nodes = append(nodes, firstNode)
		}
	}

	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].AddTimeUnix > nodes[j].AddTimeUnix
	})

	return nodes, nil
}

func checkTitle(title string) error {
	if len(title) > 255 {
		return errors.New("file too long: max 255 characters")
	}

	// TODO: special non latin characters?
	return nil
}

func filePinpinUUID(path string) (uuid.UUID, error) {
	fh, err := os.Open(path)
	if err != nil {
		return uuid.UUID{}, err
	}
	defer fh.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, fh); err != nil {
		return uuid.UUID{}, err
	}

	return digestPinpinUUID(hasher.Sum(nil)), nil
}

func titlePinpinUUID(title string) uuid.UUID {

	hasher := sha256.New()
	hasher.Write([]byte(title))

	return digestPinpinUUID(hasher.Sum(nil))
}

func digestPinpinUUID(digest []byte) (u uuid.UUID) {
	if len(digest) < 16 {
		panic("digest length must be >= than an UUID's")
	}

	copy(u[:], digest)

	sig32 := crc32.NewIEEE()
	sig32.Write([]byte("pinpin"))
	sig32.Write(u[:12])
	copy(u[12:16], sig32.Sum(nil))

	return
}

func isPinpinUUID(u uuid.UUID) bool {
	sig32 := crc32.NewIEEE()
	sig32.Write([]byte("pinpin"))
	sig32.Write(u[:12])
	return bytes.Equal(u[12:16], sig32.Sum(nil))
}

func runFfmpeg(args ...string) error {
	cmd := exec.Command("ffmpeg", args...)
	cmd.Stderr = nil
	cmd.Stdout = nil
	return cmd.Run()
}
