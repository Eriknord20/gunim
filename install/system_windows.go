//go:build windows

package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Windows installs, paths ignore letter case, and a running program
// cannot be written over, only moved.
const (
	supported   = true
	caseless    = true
	movesAside  = true
	exeSuffix   = ".exe"
	foldersHere = true
)

// Where Windows keeps a user's installed programs, the programs it
// starts with the user, and the user's kinds of file.
const (
	uninstallKeys = `Software\Microsoft\Windows\CurrentVersion\Uninstall\`
	runKey        = `Software\Microsoft\Windows\CurrentVersion\Run`
	classesKey    = `Software\Classes\`
)

// defaultDir is %LOCALAPPDATA%\Programs\<ID>.
func defaultDir(a *App) (string, error) {
	dir, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		return "", fmt.Errorf("find %%LOCALAPPDATA%%: %w", err)
	}
	return filepath.Join(dir, "Programs", a.id()), nil
}

// shortcutName is the name of the program's shortcuts: its name, as a
// file can take it.
func shortcutName(a *App) string {
	name := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) || r < ' ' {
			return '-'
		}
		return r
	}, a.Name)
	return strings.TrimRight(name, ". ") + ".lnk"
}

// startMenu is the Start menu shortcut, and desktop the one on the
// desktop.
func startMenu(a *App) (string, error) {
	dir, err := windows.KnownFolderPath(windows.FOLDERID_Programs, 0)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, shortcutName(a)), nil
}

func desktop(a *App) (string, error) {
	dir, err := windows.KnownFolderPath(windows.FOLDERID_Desktop, 0)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, shortcutName(a)), nil
}

// register adds the program to Windows: the Start menu shortcut, the
// entry under Installed apps, the kinds of file it opens, and, as
// picked, the shortcut on the desktop and the start with the user. What
// was not picked is taken away, as after an install that had it.
func register(a *App, in Installation) error {
	menu, err := startMenu(a)
	if err != nil {
		return err
	}
	if err := shortcut(menu, in.Exe, a.Description); err != nil {
		return fmt.Errorf("make the Start menu shortcut: %w", err)
	}
	if desk, err := desktop(a); err == nil {
		if in.Chose(PickDesktop) {
			if err := shortcut(desk, in.Exe, a.Description); err != nil {
				return fmt.Errorf("make the desktop shortcut: %w", err)
			}
		} else {
			_ = os.Remove(desk)
		}
	}
	if err := listInstalled(a, in); err != nil {
		return fmt.Errorf("list %s under Installed apps: %w", a.Name, err)
	}
	args := []string(nil)
	if a.Autostart != nil {
		args = a.Autostart.Args
	}
	if err := setAutostart(a, in.Exe, args, a.Autostart != nil && in.Chose(PickAutostart)); err != nil {
		return err
	}
	changed := false
	for _, t := range a.FileTypes {
		var err error
		if in.Chose(FileTypeKey(t)) {
			err = claimType(a, t, in.Exe)
		} else {
			err = dropType(a, t)
		}
		if err != nil {
			return fmt.Errorf("register %s files: %w", strings.Join(t.Exts, " "), err)
		}
		changed = true
	}
	if a.Folders != nil {
		if err := setFolders(a, in.Exe, in.Chose(PickFolders)); err != nil {
			return fmt.Errorf("open folders with %s: %w", a.Name, err)
		}
		changed = true
	}
	if changed {
		assocChanged()
	}
	return nil
}

// The classes a folder opens by, a drive's, and This PC's, whose
// opennewwindow verb Win+E runs.
var folderClasses = []string{`Directory`, `Drive`}

const winE = `CLSID\{52205fd8-5dfb-447d-801a-d0b52f2e83e1}\shell\opennewwindow`

// folderVerb is the name of the program's verb on folders and drives.
func folderVerb(a *App) string { return a.id() + ".open" }

// folderCommand is the command line that opens folder in the program at
// exe, as a verb's command writes it.
func folderCommand(a *App, exe, folder string) string {
	var cmd strings.Builder
	cmd.WriteString(`"` + exe + `"`)
	for _, arg := range append(slices.Clone(a.Folders.Args), folder) {
		cmd.WriteString(` "` + arg + `"`)
	}
	return cmd.String()
}

// setFolders opens folders and drives with the program at exe, and Win+E
// too, or no longer. What each opened with before is kept beside the
// program's, and put back. Turned on once, it is not taken again from a
// file manager installed since, as each update registers the program
// again.
func setFolders(a *App, exe string, on bool) error {
	verb := folderVerb(a)
	for _, class := range folderClasses {
		shell := classesKey + class + `\shell`
		mine := shell + `\` + verb
		if !on {
			if err := dropFolderVerb(class, shell, mine, verb); err != nil {
				return err
			}
			continue
		}
		claimed := keyExists(mine)
		label := a.Folders.Verb
		if label == "" {
			label = "Open in " + a.Name
		}
		if err := setDefault(mine, label); err != nil {
			return err
		}
		// %V is the folder, and \. after it keeps a drive's backslash
		// from escaping the closing quote: "C:\" would read as C:".
		if err := setDefault(mine+`\command`, folderCommand(a, exe, `%V\.`)); err != nil {
			return err
		}
		if claimed {
			continue
		}
		if err := setValue(mine, "Before", getDefault(shell)); err != nil {
			return err
		}
		if err := setDefault(shell, verb); err != nil {
			return err
		}
	}
	return setWinE(a, exe, on)
}

// setWinE has Win+E open the program at exe, or no longer. The command
// it ran before is kept, and put back; the key is marked the program's,
// so it goes even once the program has moved.
func setWinE(a *App, exe string, on bool) error {
	key := classesKey + winE
	cmd := key + `\command`
	mark := getValue(key, "Owner") == a.id()
	if !on {
		if !mark {
			return nil
		}
		before := getValue(key, "Before")
		if err := deleteTree(key); err != nil {
			return err
		}
		if before == "" {
			return nil
		}
		if err := setDefault(cmd, before); err != nil {
			return err
		}
		return setValue(cmd, "DelegateExecute", "")
	}
	if !mark {
		if err := setValue(key, "Before", getDefault(cmd)); err != nil {
			return err
		}
		if err := setValue(key, "Owner", a.id()); err != nil {
			return err
		}
	}
	if err := setDefault(cmd, folderCommand(a, exe, "")); err != nil {
		return err
	}
	// Empty, so Explorer's own handler for the verb is not run instead.
	return setValue(cmd, "DelegateExecute", "")
}

// dropFolderVerb takes the program's verb off a class's shell key, and
// puts back the verb it opened by before, where that is still there.
func dropFolderVerb(class, shell, mine, verb string) error {
	before := getValue(mine, "Before")
	if before != "" && !classKeyExists(class+`\shell\`+before) {
		before = ""
	}
	if getDefault(shell) == verb {
		k, err := registry.OpenKey(registry.CURRENT_USER, shell, registry.SET_VALUE)
		if err == nil {
			if before != "" {
				err = k.SetStringValue("", before)
			} else {
				err = k.DeleteValue("")
			}
			_ = k.Close()
		}
		if err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
	}
	return deleteTree(mine)
}

// keyExists reports whether the user's key path is there.
func keyExists(path string) bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	_ = k.Close()
	return true
}

// classKeyExists reports whether the class key path is there, the
// user's or the machine's.
func classKeyExists(path string) bool {
	k, err := registry.OpenKey(registry.CLASSES_ROOT, path, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	_ = k.Close()
	return true
}

// setValue sets the value name of the user's key path, making it.
func setValue(path, name, value string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer func() { _ = k.Close() }()
	return k.SetStringValue(name, value)
}

// getValue is the value name of the user's key path, or "".
func getValue(path, name string) string {
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer func() { _ = k.Close() }()
	v, _, _ := k.GetStringValue(name)
	return v
}

// listInstalled writes the program's entry under Installed apps.
func listInstalled(a *App, in Installation) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, uninstallKeys+a.id(), registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer func() { _ = k.Close() }()
	size := uint32(0)
	_ = filepath.WalkDir(in.Dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				size += uint32(fi.Size() / 1024)
			}
		}
		return nil
	})
	values := []struct{ name, value string }{
		{"DisplayName", a.Name},
		{"DisplayVersion", strings.TrimPrefix(a.Version, "v")},
		{"DisplayIcon", in.Exe},
		{"InstallLocation", in.Dir},
		{"InstallDate", time.Now().Format("20060102")},
		{"UninstallString", `"` + in.Exe + `" -uninstall`},
		{"QuietUninstallString", `"` + in.Exe + `" -uninstall -quiet`},
	}
	if a.Publisher != "" {
		values = append(values, struct{ name, value string }{"Publisher", a.Publisher})
	}
	if a.URL != "" {
		values = append(values, struct{ name, value string }{"URLInfoAbout", a.URL})
	}
	for _, v := range values {
		if err := k.SetStringValue(v.name, v.value); err != nil {
			return err
		}
	}
	for _, v := range []struct {
		name  string
		value uint32
	}{{"NoModify", 1}, {"NoRepair", 1}, {"EstimatedSize", size}} {
		if err := k.SetDWordValue(v.name, v.value); err != nil {
			return err
		}
	}
	return nil
}

// progID is the name Windows knows the program's handling of t by.
func progID(a *App, t FileType) string {
	return a.id() + "." + strings.ToLower(strings.TrimPrefix(t.Exts[0], "."))
}

// claimType registers the program as one that opens files of kind t,
// and as the one that does, when t is Default and no other program has
// claimed it.
func claimType(a *App, t FileType, exe string) error {
	id := progID(a, t)
	name := t.Name
	if name == "" {
		name = a.Name + " file"
	}
	if err := setDefault(classesKey+id, name); err != nil {
		return err
	}
	if err := setDefault(classesKey+id+`\DefaultIcon`, `"`+exe+`",0`); err != nil {
		return err
	}
	if err := setDefault(classesKey+id+`\shell\open\command`, `"`+exe+`" "%1"`); err != nil {
		return err
	}
	app := classesKey + `Applications\` + filepath.Base(exe)
	if err := setDefault(app+`\shell\open\command`, `"`+exe+`" "%1"`); err != nil {
		return err
	}
	for _, ext := range t.Exts {
		ext = strings.ToLower(ext)
		k, _, err := registry.CreateKey(registry.CURRENT_USER, classesKey+ext+`\OpenWithProgids`, registry.SET_VALUE)
		if err != nil {
			return err
		}
		err = k.SetStringValue(id, "")
		_ = k.Close()
		if err != nil {
			return err
		}
		if s, _, err := registry.CreateKey(registry.CURRENT_USER, app+`\SupportedTypes`, registry.SET_VALUE); err == nil {
			_ = s.SetStringValue(ext, "")
			_ = s.Close()
		}
		if t.Default {
			// Claimed by another program for this user or for the
			// machine, as the merged view shows it.
			now := mergedDefault(ext)
			if now == "" || now == id {
				if err := setDefault(classesKey+ext, id); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// dropType takes away what claimType adds.
func dropType(a *App, t FileType) error {
	id := progID(a, t)
	for _, ext := range t.Exts {
		ext = strings.ToLower(ext)
		if k, err := registry.OpenKey(registry.CURRENT_USER, classesKey+ext+`\OpenWithProgids`, registry.SET_VALUE); err == nil {
			_ = k.DeleteValue(id)
			_ = k.Close()
		}
		if getDefault(classesKey+ext) == id {
			if k, err := registry.OpenKey(registry.CURRENT_USER, classesKey+ext, registry.SET_VALUE); err == nil {
				_ = k.DeleteValue("")
				_ = k.Close()
			}
		}
	}
	return deleteTree(classesKey + id)
}

// setDefault sets the default value of the user's key path, making it.
func setDefault(path, value string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer func() { _ = k.Close() }()
	return k.SetStringValue("", value)
}

// getDefault is the default value of the user's key path, or "".
func getDefault(path string) string {
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer func() { _ = k.Close() }()
	v, _, _ := k.GetStringValue("")
	return v
}

// mergedDefault is the default value of key path under
// HKEY_CLASSES_ROOT, the user's classes over the machine's, or "".
func mergedDefault(path string) string {
	k, err := registry.OpenKey(registry.CLASSES_ROOT, path, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer func() { _ = k.Close() }()
	v, _, _ := k.GetStringValue("")
	return v
}

// commandProgram is the program a command line starts, as claimType
// writes it: its first argument, quoted or not.
func commandProgram(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if rest, ok := strings.CutPrefix(cmd, `"`); ok {
		exe, _, _ := strings.Cut(rest, `"`)
		return exe
	}
	exe, _, _ := strings.Cut(cmd, " ")
	return exe
}

// deleteTree deletes the user's key path and every key under it.
func deleteTree(path string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.ENUMERATE_SUB_KEYS)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	names, err := k.ReadSubKeyNames(-1)
	_ = k.Close()
	if err != nil {
		return err
	}
	for _, n := range names {
		if err := deleteTree(path + `\` + n); err != nil {
			return err
		}
	}
	if err := registry.DeleteKey(registry.CURRENT_USER, path); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// assocChanged tells the shell the kinds of file changed, so Explorer
// shows the new icons and Open with lists.
func assocChanged() {
	const shcneAssocChanged = 0x08000000
	proc := windows.NewLazySystemDLL("shell32.dll").NewProc("SHChangeNotify")
	if proc.Find() == nil {
		_, _, _ = proc.Call(shcneAssocChanged, 0, 0, 0)
	}
}

// shortcut makes a shortcut at lnk to exe, through PowerShell's Windows
// Script Host object: the shell's own way to write one, with no COM
// written here. One there already is written over, as one an older
// installer made can lead to where the program was.
func shortcut(lnk, exe, about string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		home = filepath.Dir(exe)
	}
	// The values reach the script through its environment, so no
	// character in a name or a path can end a string in it.
	cmd := powershell("$s = (New-Object -ComObject WScript.Shell).CreateShortcut($env:GUNIM_LNK); "+
		"$s.TargetPath = $env:GUNIM_EXE; $s.WorkingDirectory = $env:GUNIM_HOME; "+
		"$s.IconLocation = $env:GUNIM_EXE + ',0'; $s.Description = $env:GUNIM_ABOUT; $s.Save()",
		"GUNIM_LNK="+lnk, "GUNIM_EXE="+exe, "GUNIM_HOME="+home, "GUNIM_ABOUT="+about)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// powershell is PowerShell running script, with no window, with env
// added to its environment, in a folder that is not the program's, so
// it holds nothing there open.
func powershell(script string, env ...string) *exec.Cmd {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	cmd.Env = append(os.Environ(), env...)
	cmd.Dir = os.TempDir()
	return cmd
}

// setAutostart starts the program at exe with args when the user signs
// in, or no longer.
func setAutostart(a *App, exe string, args []string, on bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open the programs Windows starts: %w", err)
	}
	defer func() { _ = k.Close() }()
	if !on {
		if err := k.DeleteValue(a.id()); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
		return nil
	}
	line := `"` + exe + `"`
	for _, arg := range args {
		line += " " + windows.EscapeArg(arg)
	}
	return k.SetStringValue(a.id(), line)
}

// detect reads what an install that kept no record of itself has on
// the system: the shortcut on the desktop and the start with the user.
func detect(a *App, _ string) map[string]bool {
	out := map[string]bool{}
	if !a.NoDesktop {
		if desk, err := desktop(a); err == nil {
			_, err := os.Stat(desk)
			out[PickDesktop] = err == nil
		}
	}
	if a.Autostart != nil {
		if k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE); err == nil {
			_, _, err := k.GetStringValue(a.id())
			out[PickAutostart] = err == nil
			_ = k.Close()
		}
	}
	if a.Folders != nil {
		out[PickFolders] = getDefault(classesKey+`Directory\shell`) == folderVerb(a)
	}
	return out
}

// unregister takes away all that register adds.
func unregister(a *App, in Installation) error {
	if menu, err := startMenu(a); err == nil {
		_ = os.Remove(menu)
	}
	if desk, err := desktop(a); err == nil {
		_ = os.Remove(desk)
	}
	_ = setAutostart(a, in.Exe, nil, false)
	if a.Folders != nil {
		if err := setFolders(a, in.Exe, false); err != nil {
			return err
		}
		assocChanged()
	}
	for _, t := range a.FileTypes {
		if err := dropType(a, t); err != nil {
			return err
		}
	}
	if len(a.FileTypes) > 0 {
		// The key is by the program's file name, which another program
		// can have: it goes only while it opens this one.
		app := classesKey + `Applications\` + filepath.Base(in.Exe)
		if a.ours(in, commandProgram(getDefault(app+`\shell\open\command`))) {
			_ = deleteTree(app)
		}
		assocChanged()
	}
	if err := deleteTree(uninstallKeys + a.id()); err != nil {
		return err
	}
	return nil
}

// removeFiles takes the files away, and the folders under dir and dir
// itself that leaves empty. A running program's file, and a library it
// has loaded, cannot be taken away: PowerShell takes those once this
// program has ended.
func removeFiles(files []string, dir string) error {
	// A folder this program is in cannot be taken away.
	if wd, err := os.Getwd(); err == nil && strings.HasPrefix(strings.ToLower(wd), strings.ToLower(dir)) {
		_ = os.Chdir(os.TempDir())
	}
	var left []string
	for _, f := range files {
		if err := os.Remove(f); err != nil && !errors.Is(err, fs.ErrNotExist) {
			left = append(left, f)
		}
	}
	pruneEmpty(dir)
	if err := os.Remove(dir); err == nil || len(left) == 0 {
		return nil
	}
	return removeAfter(left, dir)
}

// removeWhenEnded takes the file at path away: at once, or, where this
// program runs from it, once it has ended.
func removeWhenEnded(path string) {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		_ = removeAfter([]string{path}, "")
	}
}

// removeAfter has PowerShell take the files away once this program has
// ended, and then, where dir is set, the folders under dir and dir
// itself that leaves empty.
func removeAfter(files []string, dir string) error {
	// The paths reach the script through its environment, split on |,
	// which no Windows path holds.
	script := fmt.Sprintf("Wait-Process -Id %d -ErrorAction SilentlyContinue; "+
		"$files = $env:GUNIM_FILES -split '\\|'; "+
		"for ($i = 0; $i -lt 20; $i++) { Remove-Item -LiteralPath $files -Force -ErrorAction SilentlyContinue; "+
		"if (-not ($files | Where-Object { Test-Path -LiteralPath $_ })) { break }; Start-Sleep -Milliseconds 500 }; ",
		os.Getpid())
	if dir != "" {
		script += "Get-ChildItem -LiteralPath $env:GUNIM_DIR -Recurse -Directory | Sort-Object { $_.FullName.Length } -Descending | " +
			"Where-Object { -not (Get-ChildItem -LiteralPath $_.FullName -Force) } | Remove-Item -Force; " +
			"if (-not (Get-ChildItem -LiteralPath $env:GUNIM_DIR -Force)) { Remove-Item -LiteralPath $env:GUNIM_DIR -Force }"
	}
	cmd := powershell(script, "GUNIM_FILES="+strings.Join(files, "|"), "GUNIM_DIR="+dir)
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
	return cmd.Start()
}

// freeSpace is how many bytes the user may write to the volume dir is
// on.
func freeSpace(dir string) (int64, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var free, total, all uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &all); err != nil {
		return 0, err
	}
	return int64(free), nil
}

// watchProcess watches the process pid: alive reports whether it still
// runs, and done lets it go. The process is held from now, so another
// given its number once it has ended is not taken for it.
func watchProcess(pid int) (alive func() bool, done func()) {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// Gone, or another user's, which no program of this user's
		// install is.
		return func() bool { return false }, func() {}
	}
	alive = func() bool {
		ev, err := windows.WaitForSingleObject(h, 0)
		return err == nil && ev == uint32(windows.WAIT_TIMEOUT)
	}
	return alive, func() { _ = windows.CloseHandle(h) }
}

// running lists the processes running the program at exe.
func running(exe string) ([]int, error) {
	procs, err := runningParents(exe)
	if err != nil {
		return nil, err
	}
	pids := make([]int, 0, len(procs))
	for pid := range procs {
		pids = append(pids, pid)
	}
	return pids, nil
}

// runningParents lists the processes running the program at exe, each
// with the process that started it.
func runningParents(exe string) (map[int]int, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	procs := map[int]int{}
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if !strings.EqualFold(windows.UTF16ToString(e.ExeFile[:]), filepath.Base(exe)) {
			continue
		}
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, e.ProcessID)
		if err != nil {
			continue
		}
		buf := make([]uint16, windows.MAX_LONG_PATH)
		n := uint32(len(buf))
		err = windows.QueryFullProcessImageName(h, 0, &buf[0], &n)
		_ = windows.CloseHandle(h)
		if err == nil && samePath(windows.UTF16ToString(buf[:n]), exe) {
			procs[int(e.ProcessID)] = int(e.ParentProcessID)
		}
	}
	return procs, nil
}

// launch starts the program at exe with args, on its own, with env
// added to this program's environment.
//
// Detached, it is out of reach of a Ctrl+C in this program's console.
// It is not put in a new process group: Windows has such a program
// ignore Ctrl+C, and every program it starts would too, as a terminal's
// shells.
func launch(exe string, args []string, env ...string) error {
	cmd := exec.Command(exe, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
