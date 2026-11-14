package text

import (
	_ "embed"
	"sync"
)

// Inter 4.1, regular and semibold, by The Inter Project Authors, under
// the SIL Open Font License 1.1 in fonts/LICENSE-Inter.txt. Only Inter
// reads them, so a program that never calls it leaves them out.
var (
	//go:embed fonts/Inter-Regular.ttf
	interRegular []byte
	//go:embed fonts/Inter-SemiBold.ttf
	interSemibold []byte
)

var (
	interOnce  sync.Once
	interFaces [2]*Face
)

// Inter returns Inter, the face the touch kit sets text in, at its
// regular weight or semibold. It is built into the program only where
// the program calls Inter, and read the first time it is called.
func Inter(semibold bool) *Face {
	interOnce.Do(func() {
		for i, data := range [][]byte{interRegular, interSemibold} {
			f, err := Parse(data)
			if err != nil {
				panic("text: the built-in Inter font fails to parse: " + err.Error())
			}
			interFaces[i] = f
		}
	})
	if semibold {
		return interFaces[1]
	}
	return interFaces[0]
}
