package wasapi

import "golang.org/x/sys/windows"

// notePropertyChanged counts a device's format set anew. On 32-bit x86
// Windows passes the PROPERTYKEY's 20 bytes on the stack, as five
// words, which the callback must take to leave the stack as it was.
func notePropertyChanged(_, _, k0, k1, k2, k3, pid uintptr) uintptr {
	f := pkeyDeviceFormat.fmtid
	g := windows.GUID{Data1: uint32(k0), Data2: uint16(k1), Data3: uint16(k1 >> 16)}
	for i := range 4 {
		g.Data4[i] = byte(k2 >> (8 * i))
		g.Data4[4+i] = byte(k3 >> (8 * i))
	}
	if g == f && uint32(pid) == pkeyDeviceFormat.pid {
		notes.formats.Add(1)
	}
	return sOK
}
