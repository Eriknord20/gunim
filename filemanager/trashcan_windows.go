//go:build windows && (amd64 || arm64)

package filemanager

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

var procSHEmptyRecycleBinW = shell32.NewProc("SHEmptyRecycleBinW")

// SHEmptyRecycleBin's flags, IFileOperation's DeleteItem by its place in
// the table, and what SHEmptyRecycleBin returns for a bin already empty.
const (
	sherbNoConfirmation = 0x1
	sherbNoProgressUI   = 0x2
	sherbNoSound        = 0x4
	vtDeleteItem        = 18
	eUnexpected         = 0x8000FFFF
)

// TrashItems implements [TrashCan]: what the shell lists in the Recycle
// Bin, of every drive.
func (recycleBin) TrashItems(ctx context.Context) ([]TrashItem, error) {
	var out []TrashItem
	err := onCOM(func() error {
		return eachRecycled(func(r recycled) bool {
			release(r.item)
			if ctx.Err() != nil {
				return false
			}
			if r.kept == "" {
				return true
			}
			out = append(out, binTrashItem(r))
			return true
		})
	})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// binTrashItem is r as the window lists it. The bin shows a name without
// its extension where Explorer hides extensions; the file it keeps the
// item as has it.
func binTrashItem(r recycled) TrashItem {
	path := r.path
	if r.extKnown && !strings.EqualFold(filepath.Ext(path), r.ext) {
		path += r.ext
	}
	it := TrashItem{ID: r.kept, Name: filepath.Base(path), Original: path, Deleted: r.when, Size: -1}
	if info, err := os.Lstat(r.kept); err == nil {
		it.Dir = info.IsDir()
		if !it.Dir {
			it.Size = info.Size()
		}
	}
	return it
}

// findKept returns the IShellItem2 of the item the Recycle Bin keeps at
// kept. The caller releases it.
func findKept(kept string) (unsafe.Pointer, error) {
	var found unsafe.Pointer
	err := eachRecycled(func(r recycled) bool {
		if found == nil && strings.EqualFold(r.kept, kept) {
			found = r.item
			return false
		}
		release(r.item)
		return true
	})
	if err != nil {
		if found != nil {
			release(found)
		}
		return nil, err
	}
	if found == nil {
		return nil, fmt.Errorf("the Recycle Bin no longer holds %s; it may have been restored or emptied", kept)
	}
	return found, nil
}

// RestoreItem implements [TrashCan].
func (recycleBin) RestoreItem(it TrashItem) error {
	return restoreTo(it.Original, func() (unsafe.Pointer, error) { return findKept(it.ID) })
}

// PurgeItem implements [TrashCan] with the shell, which deletes an item
// in the Recycle Bin for good, and its record with it.
func (recycleBin) PurgeItem(ctx context.Context, it TrashItem) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return onCOM(func() error {
		item, err := findKept(it.ID)
		if err != nil {
			return err
		}
		defer release(item)
		return fileOp("deleting "+it.Name, func(op unsafe.Pointer) uint32 {
			return comCall(op, vtDeleteItem, uintptr(item), 0)
		})
	})
}

// EmptyTrash implements [TrashCan]: the Recycle Bin of every drive.
func (recycleBin) EmptyTrash(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	hr, _, _ := procSHEmptyRecycleBinW.Call(0, 0, sherbNoConfirmation|sherbNoProgressUI|sherbNoSound)
	if failed(uint32(hr)) && uint32(hr) != eUnexpected {
		return fmt.Errorf("emptying the Recycle Bin failed with HRESULT %#x", uint32(hr))
	}
	return nil
}
