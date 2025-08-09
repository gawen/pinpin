package pinpin

type Command = byte

const (
	CommandUploadFile         Command = 0x01
	CommandPing               Command = 0x02
	CommandGetSDSize          Command = 0x04
	CommandUpdatePlaylist     Command = 0x06
	CommandEndSynchronization Command = 0x09
	CommandGetNumberOfFiles   Command = 0x0b
	CommandGetFileInformation Command = 0x0c
	CommandGetFile            Command = 0x0d
)
