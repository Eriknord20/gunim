//go:build windows && !386

package wasapi

// notePropertyChanged counts a device's format set anew. On a 64-bit
// system Windows passes the PROPERTYKEY, of 20 bytes, by its address.
func notePropertyChanged(_, _, key uintptr) uintptr {
	if key != 0 && *Ptr[propertyKey](key) == pkeyDeviceFormat {
		notes.formats.Add(1)
	}
	return sOK
}
