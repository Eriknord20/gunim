//go:build linux || darwin

package desktop

import (
	"errors"
	"time"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/internal/gl"
)

// Windows alone presents through DXGI; see present_windows.go. Here a
// window presents through OpenGL, by swapping its buffers.

var errNoDXGI = errors.New("desktop: presenting through DXGI is for Windows")

func (d *Driver) presentsThroughDXGI() bool { return false }

type presenter struct{}

func (w *Window) startPresenter(gl.Context) (*presenter, error) { return nil, errNoDXGI }

func (p *presenter) begin(int, int) (uint32, error) { return 0, errNoDXGI }
func (p *presenter) present([]geom.Rect) error      { return errNoDXGI }
func (p *presenter) close()                         {}
func (p *presenter) lag() (time.Duration, bool)     { return 0, false }
