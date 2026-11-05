// Command webtest runs driver/web's tests in a real browser, which they need,
// and exits with their result.
//
//	go run ./tools/webtest
//
// It builds the tests for js, serves them, opens the page in headless Chrome and
// prints what the tests print. Chrome is told its page has window focus, as some
// tests need that, and a test that skips fails the run, so a pass means all of them
// ran. It talks to Chrome over --remote-debugging-pipe, which takes no WebSocket
// client, and that pipe is not there on Windows.
//
// With -serve it builds and serves the page and stops there, to open in any other
// browser or on a phone. The result is then on the page, and in window.testResult.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// page runs the tests and keeps what they print in window.testResult.
const page = `<!doctype html><meta charset="utf-8"><title>driver/web tests</title>
<canvas id="gunim" style="width:320px;height:240px"></canvas><pre id="output" style="white-space:pre-wrap"></pre>
<script src="wasm_exec.js"></script><script>
window.testResult = {done: false, code: null, output: "", read: 0};
const decoder = new TextDecoder(), write = globalThis.fs.writeSync;
const add = s => { testResult.output += s; document.querySelector("#output").textContent += s; };
globalThis.fs.writeSync = (fd, data) => { add(decoder.decode(data)); return write(fd, data); };
const go = new Go();
go.argv = ["web.test", "-test.v"];
go.exit = code => { testResult.code = code; testResult.done = true; };
WebAssembly.instantiateStreaming(fetch("web.test.wasm"), go.importObject)
  .then(r => go.run(r.instance))
  .catch(e => { add(String(e)); go.exit(1); });
</script>`

// progress asks the page for what the tests printed since it last asked.
const progress = `window.testResult ? JSON.stringify({
  done: testResult.done, code: testResult.code,
  more: testResult.output.slice(testResult.read, testResult.read = testResult.output.length),
}) : ""`

func main() {
	chrome := flag.String("chrome", "", "the browser to run, or the first Chrome or Chromium on the PATH")
	serve := flag.String("serve", "", "only serve the page at this address, such as 127.0.0.1:8784")
	timeout := flag.Duration("timeout", 5*time.Minute, "how long the tests may take")
	flag.Parse()
	if err := run(*chrome, *serve, *timeout); err != nil {
		log.Fatal("webtest: ", err)
	}
}

func run(chrome, serve string, timeout time.Duration) error {
	dir, err := os.MkdirTemp("", "gunim-webtest")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := build(ctx, dir); err != nil {
		return err
	}
	handler := http.FileServer(http.Dir(dir))
	if serve != "" {
		fmt.Printf("open http://%s/\n", serve)
		return http.ListenAndServe(serve, handler)
	}
	l, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer func() { _ = l.Close() }()
	go func() { _ = http.Serve(l, handler) }()
	err = runInChrome(ctx, chrome, dir, "http://"+l.Addr().String()+"/")
	if ctx.Err() != nil {
		return fmt.Errorf("no result in %v", timeout)
	}
	return err
}

// build leaves the tests, Go's loader for them and the page in dir.
func build(ctx context.Context, dir string) error {
	test := exec.CommandContext(ctx, "go", "test", "-c", "-o", filepath.Join(dir, "web.test.wasm"), "./driver/web")
	test.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	test.Stdout, test.Stderr = os.Stdout, os.Stderr
	if err := test.Run(); err != nil {
		return fmt.Errorf("building the tests: %w", err)
	}
	root, err := exec.CommandContext(ctx, "go", "env", "GOROOT").Output()
	if err != nil {
		return err
	}
	loader, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(root)), "lib", "wasm", "wasm_exec.js"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_exec.js"), loader, 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "index.html"), []byte(page), 0o600)
}

func findChrome() (string, error) {
	for _, name := range []string{
		"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", errors.New("no Chrome found: name one with -chrome")
}

func runInChrome(ctx context.Context, chrome, dir, url string) error {
	if runtime.GOOS == "windows" {
		return errors.New("no debugging pipe to Chrome on Windows: use -serve and open the page")
	}
	if chrome == "" {
		var err error
		if chrome, err = findChrome(); err != nil {
			return err
		}
	}
	// Chrome reads commands from file descriptor 3 and answers on 4.
	commandsR, commandsW, err := os.Pipe()
	if err != nil {
		return err
	}
	answersR, answersW, err := os.Pipe()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, chrome, "--headless=new", "--remote-debugging-pipe",
		"--user-data-dir="+filepath.Join(dir, "profile"), "--no-first-run", "--no-default-browser-check",
		"--no-sandbox", "--use-angle=swiftshader", "--enable-unsafe-swiftshader", "--window-size=1280,800",
		"about:blank")
	cmd.ExtraFiles = []*os.File{commandsR, answersW}
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = commandsR.Close()
	_ = answersW.Close()
	defer func() {
		_ = commandsW.Close() // Chrome ends when this closes.
		_ = cmd.Wait()
	}()
	return runTests(&debugger{to: commandsW, from: bufio.NewReader(answersR)}, url)
}

// runTests opens the page with window focus, and prints what it prints.
func runTests(c *debugger, url string) error {
	var target struct{ TargetID string }
	if err := c.call("", "Target.createTarget", map[string]any{"url": "about:blank"}, &target); err != nil {
		return err
	}
	var attached struct{ SessionID string }
	if err := c.call("", "Target.attachToTarget", map[string]any{"targetId": target.TargetID, "flatten": true}, &attached); err != nil {
		return err
	}
	session := attached.SessionID
	if err := c.call(session, "Emulation.setFocusEmulationEnabled", map[string]any{"enabled": true}, nil); err != nil {
		return err
	}
	if err := c.call(session, "Page.navigate", map[string]any{"url": url}, nil); err != nil {
		return err
	}
	var output strings.Builder
	for {
		var answer struct{ Result struct{ Value string } }
		if err := c.call(session, "Runtime.evaluate", map[string]any{"expression": progress}, &answer); err != nil {
			return err
		}
		var now struct {
			Done bool
			Code int
			More string
		}
		if answer.Result.Value != "" {
			if err := json.Unmarshal([]byte(answer.Result.Value), &now); err != nil {
				return err
			}
		}
		fmt.Print(now.More)
		output.WriteString(now.More)
		if now.Done {
			if now.Code != 0 {
				return fmt.Errorf("the tests failed, with code %d", now.Code)
			}
			// A skipped test didn't run, and a pass here says they all did.
			if n := strings.Count(output.String(), "--- SKIP"); n > 0 {
				return fmt.Errorf("%d tests skipped", n)
			}
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// debugger speaks Chrome's DevTools protocol over the pipe: a JSON message
// followed by a zero byte each way.
type debugger struct {
	to   *os.File
	from *bufio.Reader
	id   int
}

// call sends a command to the session, or to the browser if that is empty, and
// reads its answer into out. It passes over the events Chrome sends between.
func (c *debugger) call(session, method string, params, out any) error {
	c.id++
	message, err := json.Marshal(struct {
		ID        int    `json:"id"`
		SessionID string `json:"sessionId,omitempty"`
		Method    string `json:"method"`
		Params    any    `json:"params"`
	}{c.id, session, method, params})
	if err != nil {
		return err
	}
	if _, err := c.to.Write(append(message, 0)); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	for {
		line, err := c.from.ReadBytes(0)
		if err != nil {
			return fmt.Errorf("%s: %w", method, err)
		}
		var answer struct {
			ID     int
			Result json.RawMessage
			Error  *struct{ Message string }
		}
		if err := json.Unmarshal(line[:len(line)-1], &answer); err != nil {
			return err
		}
		if answer.ID != c.id {
			continue
		}
		if answer.Error != nil {
			return fmt.Errorf("%s: %s", method, answer.Error.Message)
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(answer.Result, out)
	}
}
