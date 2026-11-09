//go:build windows && (amd64 || arm64)

package filemanager

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procSHEmptyRecycleBinW     = shell32.NewProc("SHEmptyRecycleBinW")
	procSHQueryRecycleBinW     = shell32.NewProc("SHQueryRecycleBinW")
	procSHUpdateRecycleBinIcon = shell32.NewProc("SHUpdateRecycleBinIcon")
)

// binChanged has the shell show the Recycle Bin as full or empty again, as
// an item moved out of it by a rename tells it nothing.
func binChanged() { _, _, _ = procSHUpdateRecycleBinIcon.Call() }

// SHEmptyRecycleBin's flags, IFileOperation's DeleteItem by its place in
// the table, and what SHEmptyRecycleBin returns for a bin already empty.
const (
	sherbNoConfirmation = 0x1
	sherbNoProgressUI   = 0x2
	sherbNoSound        = 0x4
	vtDeleteItem        = 18
	eUnexpected         = 0x8000FFFF
)

// shQueryRBInfo is SHQUERYRBINFO as 64-bit Windows lays it out.
type shQueryRBInfo struct {
	size     uint32
	bytes    int64
	numItems int64
}

// TrashItems implements [TrashCan]: what the shell lists in the Recycle
// Bin, of every drive, each item as its record says. An item that cannot
// be read fails the whole, as Empty deletes it too, so a list without it
// would not be all the bin holds.
func (recycleBin) TrashItems(ctx context.Context) ([]TrashItem, error) {
	var out []TrashItem
	var bad error
	err := onCOM(func() error {
		return eachRecycled(func(r recycled) bool {
			release(r.item)
			if ctx.Err() != nil {
				return false
			}
			it, err := binTrashItem(r)
			if err != nil {
				bad = err
				return false
			}
			out = append(out, it)
			return true
		})
	})
	if err != nil {
		return nil, err
	}
	if bad != nil {
		return nil, bad
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// binTrashItem is r as the window lists it: where it came from and when it
// went, as the item's record in the bin says.
func binTrashItem(r recycled) (TrashItem, error) {
	if r.kept == "" {
		return TrashItem{}, fmt.Errorf("the Recycle Bin lists %s with no file it keeps it as", r.path)
	}
	from, deleted, err := readBinRecord(r.kept)
	if err != nil {
		return TrashItem{}, err
	}
	info, err := os.Lstat(r.kept)
	if err != nil {
		return TrashItem{}, fmt.Errorf("reading %s in the Recycle Bin: %w", from, err)
	}
	it := TrashItem{ID: r.kept, Name: filepath.Base(from), Original: from, Deleted: deleted, Size: -1, Dir: info.IsDir()}
	if !it.Dir {
		it.Size = info.Size()
	}
	return it, nil
}

// sameItem checks that the Recycle Bin keeps at it.ID the item it was
// listed as: the record says it came from the same place at the same time.
// The bin names the file it keeps an item as at random, but a name freed
// by a restore may be taken by an item deleted after, which this tells
// apart.
func sameItem(it TrashItem) error {
	if err := inBin(it.ID); err != nil {
		return err
	}
	from, deleted, err := readBinRecord(it.ID)
	if err != nil {
		return err
	}
	if !strings.EqualFold(from, it.Original) || !deleted.Equal(it.Deleted) {
		return fmt.Errorf("the Recycle Bin no longer holds the %s listed; read it again", it.Name)
	}
	return nil
}

// keptItem returns the IShellItem of the file the Recycle Bin keeps an
// item as, at kept. The caller releases it.
func keptItem(kept string) (unsafe.Pointer, error) {
	name, err := windows.UTF16PtrFromString(kept)
	if err != nil {
		return nil, err
	}
	var item unsafe.Pointer
	hr, _, _ := procSHCreateItemFromParsingName.Call(uintptr(unsafe.Pointer(name)), 0,
		uintptr(unsafe.Pointer(&iidShellItem)), uintptr(unsafe.Pointer(&item)))
	if failed(uint32(hr)) {
		return nil, fmt.Errorf("finding %s in the Recycle Bin: HRESULT %#x", kept, uint32(hr))
	}
	return item, nil
}

// RestoreItem implements [TrashCan]: the item goes back where it came
// from, where nothing may be.
func (recycleBin) RestoreItem(it TrashItem) error {
	if err := sameItem(it); err != nil {
		return err
	}
	return restoreKept(it.ID, it.Original)
}

// PurgeItem implements [TrashCan] with the shell, which deletes an item in
// the Recycle Bin for good; its record goes after it.
func (recycleBin) PurgeItem(ctx context.Context, it TrashItem) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := sameItem(it); err != nil {
		return err
	}
	err := onCOM(func() error {
		item, err := keptItem(it.ID)
		if err != nil {
			return err
		}
		defer release(item)
		return fileOp("deleting "+it.Name, func(op unsafe.Pointer) uint32 {
			return comCall(op, vtDeleteItem, uintptr(item), 0)
		})
	})
	if err != nil {
		return err
	}
	binChanged()
	if err := dropRecord(it.ID); err != nil {
		return fmt.Errorf("deleting %s: %w", it.Name, err)
	}
	return nil
}

// EmptyTrash implements [TrashCan]: the Recycle Bin of every drive. The
// shell answers E_UNEXPECTED for a bin already empty, and for some
// failures too, so that answer counts only where the bin then is empty.
func (recycleBin) EmptyTrash(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	hr, _, _ := procSHEmptyRecycleBinW.Call(0, 0, sherbNoConfirmation|sherbNoProgressUI|sherbNoSound)
	binChanged()
	switch {
	case !failed(uint32(hr)):
		return nil
	case uint32(hr) == eUnexpected:
		n, err := binItems()
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		return fmt.Errorf("emptying the Recycle Bin failed; it still holds %s", plural(int(n), "item"))
	}
	return fmt.Errorf("emptying the Recycle Bin failed with HRESULT %#x", uint32(hr))
}

// binItems is how many items the Recycle Bin of every drive holds.
func binItems() (int64, error) {
	info := shQueryRBInfo{size: uint32(unsafe.Sizeof(shQueryRBInfo{}))}
	hr, _, _ := procSHQueryRecycleBinW.Call(0, uintptr(unsafe.Pointer(&info)))
	if failed(uint32(hr)) {
		return 0, fmt.Errorf("asking what the Recycle Bin holds: HRESULT %#x", uint32(hr))
	}
	return info.numItems, nil
}
