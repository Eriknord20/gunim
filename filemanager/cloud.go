package filemanager

import (
	"context"
	"io/fs"
)

// CloudKeeper is a file system that tells a cloud provider which items
// to keep on this device, as Windows' Explorer does for OneDrive. Disk
// usage offers it for items in a cloud provider's folder.
type CloudKeeper interface {
	// KeepOnDevice has the provider always keep the item at path on this
	// device with keep, and all a folder holds, downloading what is
	// online only; without keep it lets the provider free their space,
	// keeping them online only. The provider does either in its own
	// time.
	KeepOnDevice(ctx context.Context, path string, keep bool) error
}

// CloudState is how a cloud provider, such as OneDrive, keeps an item,
// as Windows' Explorer shows it beside the name.
type CloudState uint8

// The cloud states.
const (
	// CloudNone is an item outside any cloud provider's folder.
	CloudNone CloudState = iota
	// CloudOnline keeps the item's contents online only, so reading them
	// downloads them.
	CloudOnline
	// CloudLocal has the item on this device, where the provider may
	// free its space again.
	CloudLocal
	// CloudPinned always keeps the item on this device.
	CloudPinned
	// CloudFolder is a folder in a cloud provider's folder, whose own
	// state cannot be told.
	CloudFolder
)

// CloudReporter is a file system that says how a cloud provider keeps
// each item, beyond what [OnlineReporter] says of a file. A file system
// that is one is asked in its place.
type CloudReporter interface {
	// Cloud says how a cloud provider keeps the item info describes, in
	// the folder dir.
	Cloud(dir string, info fs.FileInfo) CloudState
}

// The attributes Windows gives an item a cloud provider keeps, as
// Explorer reads them: reading the contents, or for recallOnOpen
// opening the item, downloads it; pinned items are always kept on this
// device.
const (
	fileAttributeOffline            = 0x00001000
	fileAttributePinned             = 0x00080000
	fileAttributeRecallOnOpen       = 0x00040000
	fileAttributeRecallOnDataAccess = 0x00400000
)

// WindowsCloud is the cloud state of an item with the Windows attributes
// attrs, a folder with dir, in a cloud provider's folder with inCloud: for
// a file system of Windows' files read some other way than this
// computer's own, such as over SFTP from a program that sends them.
func WindowsCloud(attrs uint32, dir, inCloud bool) CloudState {
	switch {
	case attrs&(fileAttributeOffline|fileAttributeRecallOnOpen|fileAttributeRecallOnDataAccess) != 0:
		// Online first: a file pinned and not yet downloaded is read
		// only when the user asks, as any kept online.
		return CloudOnline
	case attrs&fileAttributePinned != 0:
		return CloudPinned
	case !inCloud:
		return CloudNone
	case dir:
		// A folder's own attributes say nothing of what is inside it.
		return CloudFolder
	}
	return CloudLocal
}
