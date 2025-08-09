package pinpin

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
)

// Code ported from https://github.com/djokeur/merlinator/blob/main/src/io_utils.py

type PlaylistItem struct {
	AddTimeUnix   uint32
	ChildrenCount uint16
	FavoriteOrder uint16
	FileName      string
	ID            uint16
	Kind          PlaylistItemKind
	LimitTimeUnix uint32
	Order         uint16
	ParentID      uint16
	Title         string
}

type PlaylistItemKind = uint16

const (
	PlaylistItemKindRoot           PlaylistItemKind = 0x0001
	PlaylistItemKindFolder         PlaylistItemKind = 0x0002
	PlaylistItemKindAudio          PlaylistItemKind = 0x0004
	PlaylistItemKindFolderFavorite PlaylistItemKind = 0x000a
	PlaylistItemKindMask           PlaylistItemKind = 0x000f
	PlaylistItemKindDiscoverMask   PlaylistItemKind = 0x0010
)

type PlaylistTreeNode struct {
	UUID             string              `json:"uuid"`
	Title            string              `json:"title"`
	Children         []*PlaylistTreeNode `json:"child,omitzero"`
	AddTimeUnix      uint32              `json:"add_time,omitempty"`
	LimitTimeUnixPtr *uint32             `json:"limit_time,omitempty"`
	Favorite         int                 `json:"favorite,omitempty"`
	Discover         int                 `json:"discover,omitempty"`
}

func DecodePlaylistBin(buf []byte) (pis []PlaylistItem, err error) {
	defer func() {
		if rvr := recover(); rvr != nil {
			err = fmt.Errorf("unexpected protocol: %v", rvr)
		}
	}()

	var idx int
	for idx < len(buf) {
		var pi PlaylistItem

		cur := buf[idx:]
		pi.ID = binary.LittleEndian.Uint16(cur[0:2])
		pi.ParentID = binary.LittleEndian.Uint16(cur[2:4])
		pi.Order = binary.LittleEndian.Uint16(cur[4:6])
		pi.ChildrenCount = binary.LittleEndian.Uint16(cur[6:8])
		pi.FavoriteOrder = binary.LittleEndian.Uint16(cur[8:10])
		pi.Kind = binary.LittleEndian.Uint16(cur[10:12])
		pi.LimitTimeUnix = binary.LittleEndian.Uint32(cur[12:16])
		pi.AddTimeUnix = binary.LittleEndian.Uint32(cur[16:20])
		fileNameLen := int(cur[20])
		pi.FileName = string(cur[21 : 21+fileNameLen])
		titleLen := int(cur[85])
		pi.Title = string(cur[86 : 86+titleLen])

		pis = append(pis, pi)

		idx = idx + 152
	}

	return
}

func BuildPlaylistTree(items []PlaylistItem) ([]*PlaylistTreeNode, error) {
	// search for root
	rootId, has := func() (uint16, bool) {
		for _, item := range items {
			if item.Kind == PlaylistItemKindRoot {
				return item.ID, true
			}
		}
		return 0, false
	}()
	if !has {
		return nil, fmt.Errorf("unable to find root node")
	}

	return walkPlaylist(items, rootId)
}

func walkPlaylist(items []PlaylistItem, parentId uint16) (children []*PlaylistTreeNode, err error) {
	for _, item := range items {
		if item.ID == parentId || item.ParentID != parentId {
			continue
		}

		var kind PlaylistItemKind = item.Kind & 0x000f
		discoverMask := item.Kind & PlaylistItemKindDiscoverMask

		node := new(PlaylistTreeNode)
		children = append(children, node)

		node.UUID = item.FileName
		node.Title = item.Title
		if item.Kind == PlaylistItemKindAudio {
			node.AddTimeUnix = item.AddTimeUnix
			node.LimitTimeUnixPtr = &item.LimitTimeUnix
		}

		node.Children, err = walkPlaylist(items, item.ID)
		if err != nil {
			return
		}

		if kind == PlaylistItemKindFolder || kind == PlaylistItemKindFolderFavorite {
			if node.Children == nil {
				node.Children = make([]*PlaylistTreeNode, 0)
			}
		}

		if kind == PlaylistItemKindFolderFavorite {
			node.Favorite = 1
		}

		if discoverMask != 0 {
			node.Discover = 1
		}
	}
	return
}

func MarshalPlaylistJson(nodes []*PlaylistTreeNode) ([]byte, error) {
	return json.Marshal(nodes)
}
