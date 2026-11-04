// Command gunimapk builds a gunim program into an Android APK.
//
//	go run ./tools/gunimapk -o calculator.apk ./example/calculator
//
// It builds the program as libgunim.so for each ABI asked for, with cgo
// and the NDK's clang, compiles the Java half of the Android driver,
// links a manifest that starts gunim's activity, and signs the APK.
// -install installs it on the device adb sees, and -run starts it there
// too.
//
// -name gives the APK a label, and -icon a launcher icon from a PNG. The
// launcher gets the icon as an adaptive one: the picture, its clear
// margin cut off, fills the part of the icon the launcher shows, over a
// background of the colour of the picture's own edge, or of
// -icon-background. So a launcher cuts it to its own shape, where it
// would shrink a plain icon onto a white plate.
//
// -orientation holds the screen one way, by Android's name for it:
// portrait keeps a program laid out for a phone held upright from
// turning, and stretching, as the phone tips over. By default the
// program turns with the phone.
//
// -permissions names what the program may do beyond drawing, sound and
// a light buzz: music, to read the phone's music once the user lets it,
// microphone, to record once the user lets it,
// and playback, to play on in the background, in the system's media
// controls, as gunim's App.SetNowPlaying asks. Google Play asks the
// developer why an app plays in the background, so leave playback out
// of a program that only makes sound while it is in front, as a game.
//
// -opens names file extensions the program opens, comma separated, as
// .hej,.hejprop. Android then offers the program under "Open with" and in
// the share sheet for files of those extensions, and gunim's
// input.FilesOpened brings them, at start and while it runs. The activity
// runs once, so a file opened while the program runs reaches the same
// program. Android matches on an extension only where the file's address
// holds one. For an address without one, and for a share, it matches on
// the MIME type, so -opens takes application/octet-stream, which Android
// gives an extension it does not know. The program shows up for every
// such file, and gunim passes on the ones of the named extensions. An
// extension with a MIME type of its own, as .mp3, shows up only where
// the address holds the extension.
//
// # Debug and release builds
//
// By default the APK is a debug build, signed with the debug key in
// ~/.android/debug.keystore, which gunimapk makes the first time.
// It is named for its commit and the time it was built, and shows that
// name as it starts.
//
// -keystore signs with a key of your own and makes a release build,
// closed to debuggers. Android installs an update only when it is
// signed with the same key as the APK installed, so one key kept safe
// signs every build of the program from any machine. -genkey makes the
// key first, with keytool, which asks for its passwords and for the
// name to put in it:
//
//	go run ./tools/gunimapk -keystore ~/keys/calculator.jks -genkey ./example/calculator
//	go run ./tools/gunimapk -keystore ~/keys/calculator.jks -install ./example/calculator
//
// apksigner asks for the keystore's password as it signs, or takes it
// from $GUNIMAPK_STORE_PASS, and the key's from $GUNIMAPK_KEY_PASS where
// it differs. -key names the key in the keystore, gunim by default. An
// APK signed with a new key installs only once the old one is
// uninstalled, which deletes the program's data.
//
// # Google Play
//
// An -o that ends in .aab writes an Android App Bundle, the form Google
// Play takes, instead of an APK. Google's bundletool packs it, from the
// same manifest, Java and libraries, and jarsigner signs it with the
// key from -keystore, the upload key that Play knows the developer by.
// Play signs the APKs it makes from the bundle with its own key.
//
//	go run ./tools/gunimapk -keystore ~/keys/upload.jks -id se.example.calculator \
//		-version 1.0.0 -o calculator.aab ./example/calculator
//
// -version names the build as Play shows it to users; by default a
// build is named for its commit and the time it was built. Each build's
// version code is the minutes since 1970, so each is newer than the
// last, as Play asks. gunimapk fetches bundletool 1.18.3 from GitHub the
// first time, checks it against its SHA-256, and keeps it in the user's
// cache; $BUNDLETOOL names a jar of its own instead. -install and -run
// install a bundle through bundletool, as the APKs Play would make for
// the device, signed with the debug key.
//
// Play takes only native libraries whose segments load at 16 KB
// boundaries, for phones with 16 KB memory pages, so gunimapk checks
// each library it builds and stops at one that would not load there.
//
// # Where the tools are
//
// It finds the Android SDK at $ANDROID_HOME or $ANDROID_SDK_ROOT, at the
// path ~/.androidrc gives with --sdk=, or where Android Studio installs
// it: ~/Android/Sdk on Linux, ~/Library/Android/sdk on macOS, and
// %LOCALAPPDATA%\Android\Sdk on Windows. It takes the newest NDK, build
// tools and platform installed there. Google's android tool installs
// what is missing:
//
//	android sdk install platform-tools platforms/android-36 build-tools/36.0.0 ndk/29.0.14206865
//
// Java comes from $JAVA_HOME, or the PATH.
package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/marrasen/gunim/driver/android/java"
)

// minSDK is the oldest Android the APK runs on: 8.0. targetSDK is the
// one it is built for, Android 16, which Google Play asks of new apps
// and updates since 31 August 2026. An app built for it draws under the
// system bars, which the driver does, and hears the back gesture
// through a callback, which GunimActivity registers.
const (
	minSDK    = 26
	targetSDK = 36
)

// abis maps an Android ABI to its GOARCH and its clang target.
var abis = map[string]struct{ goarch, clang string }{
	"arm64-v8a":   {"arm64", "aarch64-linux-android"},
	"x86_64":      {"amd64", "x86_64-linux-android"},
	"armeabi-v7a": {"arm", "armv7a-linux-androideabi"},
	"x86":         {"386", "i686-linux-android"},
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("gunimapk: ")
	out := flag.String("o", "", "the APK to write, or the App Bundle for Google Play, ending .aab; the package's name and .apk by default")
	id := flag.String("id", "", "the application ID; org.gunim.<name> by default")
	name := flag.String("name", "", "the application's label; the package's name by default")
	version := flag.String("version", "", "the version name users see, as 1.0.0; the commit and the time it was built by default")
	iconPNG := flag.String("icon", "", "a PNG file for the launcher's icon, square, 288 pixels or more")
	iconBG := flag.String("icon-background", "", "the colour under the launcher's icon, as #rrggbb; the colour of the icon's edge by default")
	permList := flag.String("permissions", "", "what the program may do beyond drawing and sound, comma separated: music, to read the phone's music, microphone, to record, and playback, to play on in the background with the system's media controls")
	category := flag.String("category", "", "what kind of app the program is, by Android's name, as game; a game keeps -orientation on a tablet")
	opensList := flag.String("opens", "", "file extensions the program opens through \"Open with\" and the share sheet, comma separated, as .hej,.hejprop")
	orientation := flag.String("orientation", "", "hold the screen one way, by Android's name, as portrait or landscape; it turns with the phone by default")
	abiList := flag.String("abi", "arm64-v8a,x86_64", "the ABIs to build for, comma separated")
	install := flag.Bool("install", false, "install the APK with adb")
	run := flag.Bool("run", false, "install the APK with adb and start it")
	keystore := flag.String("keystore", "", "sign a release build with a key from this keystore")
	keyAlias := flag.String("key", "gunim", "the name of the key in -keystore")
	genKey := flag.Bool("genkey", false, "make the -keystore and its key with keytool, which asks for passwords, then build")
	flag.Usage = func() {
		_, _ = fmt.Fprintf(flag.CommandLine.Output(), "usage: gunimapk [flags] package\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	pkg := flag.Arg(0)
	base := filepath.Base(pkg)
	if base == "." || base == "/" {
		wd, _ := os.Getwd()
		base = filepath.Base(wd)
	}
	if *out == "" {
		*out = base + ".apk"
	}
	if *name == "" {
		*name = base
	}
	if *id == "" {
		*id = "org.gunim." + regexp.MustCompile(`[^a-z0-9_]`).ReplaceAllString(strings.ToLower(base), "_")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	var perms []string
	if *permList != "" {
		perms = strings.Split(*permList, ",")
		for _, p := range perms {
			if _, ok := permissions[p]; !ok {
				log.Fatalf("gunimapk: no permission %q; there are music, microphone and playback", p)
			}
		}
	}
	opens, perr := parseOpens(*opensList)
	if perr != nil {
		log.Fatalf("gunimapk: %v", perr)
	}
	if *category != "" && !slices.Contains(categories, *category) {
		log.Fatalf("no category %q; there are %s", *category, strings.Join(categories, ", "))
	}
	if *orientation != "" && !slices.Contains(orientations, *orientation) {
		log.Fatalf("no orientation %q; there are %s", *orientation, strings.Join(orientations, ", "))
	}
	if *genKey && *keystore == "" {
		log.Fatal("-genkey needs -keystore, the file to make")
	}
	o := options{pkg: pkg, out: *out, id: *id, name: *name, version: *version, iconPNG: *iconPNG, iconBG: *iconBG, perms: perms,
		opens: opens, orientation: *orientation, category: *category, abis: strings.Split(*abiList, ","), keystore: *keystore, keyAlias: *keyAlias, genKey: *genKey,
		install: *install || *run, start: *run}
	err := buildAPK(ctx, o)
	stop()
	if err != nil {
		log.Fatal(err)
	}
}

// options are what the flags ask for.
type options struct {
	pkg, out, id, name string
	// version is the version name, or "" for the commit and the time.
	version string
	// iconPNG is the launcher's icon, and iconBG the colour under it.
	iconPNG, iconBG string
	perms, abis     []string
	// opens are the file extensions the program opens, as .hej.
	opens []string
	// orientation is Android's name for the one way the screen is held,
	// or "" to turn with the phone.
	orientation string
	// category is Android's name for the kind of app, as game, or "".
	category string
	// keystore holds the key that signs a release build, named
	// keyAlias; "" signs a debug build with the debug key. genKey makes
	// the keystore first.
	keystore, keyAlias string
	genKey             bool
	install, start     bool
}

// release reports whether the build is a release build.
func (o options) release() bool { return o.keystore != "" }

// bundle reports whether the build is an App Bundle, for Google Play,
// rather than an APK.
func (o options) bundle() bool { return strings.EqualFold(filepath.Ext(o.out), ".aab") }

// buildAPK builds the APK, installs it when o.install is set and starts
// it when o.start is, and removes its working directory whatever
// happens. Interrupting ctx stops the tool running.
func buildAPK(ctx context.Context, o options) error {
	b, err := newBuilder(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(b.tmp) }()
	if o.genKey {
		if err := b.genKey(o.keystore, o.keyAlias); err != nil {
			return err
		}
	}
	if err := b.build(o); err != nil {
		return err
	}
	adb := filepath.Join(b.sdk, "platform-tools", "adb")
	if o.install {
		install := func() error { return b.tool(adb, "install", "-r", o.out) }
		if o.bundle() {
			install = func() error { return b.installBundle(o.out, adb) }
		}
		if err := install(); err != nil {
			return err
		}
	}
	if o.start {
		return b.tool(adb, "shell", "am", "start", "-n", o.id+"/gunim.android.GunimActivity")
	}
	return nil
}

// builder holds where the tools are, and the directory it works in.
// Cancelling ctx stops the tool running.
type builder struct {
	ctx                                       context.Context
	sdk, ndk, buildTools, androidJar, javaBin string
	tmp                                       string
}

func newBuilder(ctx context.Context) (*builder, error) {
	b := &builder{ctx: ctx}
	places := sdkPlaces(os.Getenv, runtime.GOOS, home())
	for _, dir := range places {
		if exists(filepath.Join(dir, "platforms")) {
			b.sdk = dir
			break
		}
	}
	if b.sdk == "" {
		return nil, fmt.Errorf("no Android SDK in %s: set ANDROID_HOME, or install one with Google's android tool, "+
			"https://d.android.com/tools/agents/android-cli:\n\t%s", strings.Join(places, ", "), installHint)
	}
	var err error
	if b.ndk = os.Getenv("ANDROID_NDK_HOME"); b.ndk == "" {
		if b.ndk, err = newest(filepath.Join(b.sdk, "ndk")); err != nil {
			return nil, fmt.Errorf("no NDK: %w; android sdk list shows the NDKs to install with android sdk install ndk/<version>", err)
		}
	}
	if b.buildTools, err = newest(filepath.Join(b.sdk, "build-tools")); err != nil {
		return nil, fmt.Errorf("no build tools: %w; android sdk list shows the build tools to install with android sdk install build-tools/<version>", err)
	}
	platform, err := newest(filepath.Join(b.sdk, "platforms"))
	if err != nil {
		return nil, fmt.Errorf("no platform: %w; install one with android sdk install platforms/android-36", err)
	}
	b.androidJar = filepath.Join(platform, "android.jar")
	if jh := os.Getenv("JAVA_HOME"); jh != "" {
		b.javaBin = filepath.Join(jh, "bin")
	} else if javac, lookErr := exec.LookPath("javac"); lookErr == nil {
		b.javaBin = filepath.Dir(javac)
	} else {
		return nil, errors.New("no Java: set JAVA_HOME")
	}
	if b.tmp, err = os.MkdirTemp("", "gunimapk"); err != nil {
		return nil, err
	}
	return b, nil
}

// installHint is the android tool's command that installs what
// gunimapk needs.
const installHint = "android sdk install platform-tools platforms/android-36 build-tools/36.0.0 ndk/29.0.14206865"

// sdkPlaces returns where the Android SDK may be, in the order to look:
// where the environment says, where ~/.androidrc points Google's android
// tool, and where Android Studio installs it on goos. getenv reads the
// environment, and userHome is the user's home directory.
func sdkPlaces(getenv func(string) string, goos, userHome string) []string {
	var dirs []string
	add := func(d string) {
		if d != "" && !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	add(getenv("ANDROID_HOME"))
	add(getenv("ANDROID_SDK_ROOT"))
	if userHome == "" {
		return dirs
	}
	if rc, err := os.ReadFile(filepath.Join(userHome, ".androidrc")); err == nil {
		add(androidrcSDK(string(rc), userHome))
	}
	switch goos {
	case "darwin":
		add(filepath.Join(userHome, "Library", "Android", "sdk"))
	case "windows":
		if local := getenv("LOCALAPPDATA"); local != "" {
			add(filepath.Join(local, "Android", "Sdk"))
		}
	default:
		add(filepath.Join(userHome, "Android", "Sdk"))
		// Where gunim's own notes once put it, by hand.
		add(filepath.Join(userHome, "Android", "sdk"))
	}
	return dirs
}

// androidrcSDK returns the SDK a ~/.androidrc names with --sdk=, its ~
// taken as userHome, or "".
func androidrcSDK(rc, userHome string) string {
	for _, f := range strings.Fields(rc) {
		if v, ok := strings.CutPrefix(f, "--sdk="); ok {
			v = strings.Trim(v, `"'`)
			if rest, ok := strings.CutPrefix(v, "~"); ok {
				v = userHome + rest
			}
			return filepath.Clean(v)
		}
	}
	return ""
}

// build writes the APK o asks for.
func (b *builder) build(o options) error {
	pkg := o.pkg
	var libs []string
	for _, abi := range o.abis {
		lib, err := b.goLib(pkg, abi)
		if err != nil {
			return err
		}
		libs = append(libs, lib)
	}
	dex, dexErr := b.dex()
	if dexErr != nil {
		return dexErr
	}
	manifest := filepath.Join(b.tmp, "AndroidManifest.xml")
	if err := os.WriteFile(manifest, []byte(manifestFor(o)), 0o644); err != nil {
		return err
	}
	var res []string
	if o.iconPNG != "" {
		compiled, iconErr := b.icon(o.iconPNG, o.iconBG)
		if iconErr != nil {
			return iconErr
		}
		res = append(res, compiled)
	}
	version := o.version
	if version == "" {
		version = versionOf(b.ctx, pkg)
	}
	link := []string{"-I", b.androidJar,
		"--manifest", manifest, "--min-sdk-version", strconv.Itoa(minSDK),
		"--target-sdk-version", strconv.Itoa(targetSDK), "--version-code", strconv.FormatInt(time.Now().Unix()/60, 10),
		"--version-name", version}
	if !o.release() {
		link = append(link, "--debug-mode")
	}
	link = append(link, res...)
	files := []packed{{"classes.dex", dex}}
	for _, lib := range libs {
		rel, err := filepath.Rel(b.tmp, lib)
		if err != nil {
			return err
		}
		files = append(files, packed{filepath.ToSlash(rel), lib})
	}
	if o.bundle() {
		return b.buildBundle(o, link, files)
	}
	linked := filepath.Join(b.tmp, "linked.apk")
	if err := b.tool(filepath.Join(b.buildTools, "aapt2"), append([]string{"link", "-o", linked}, link...)...); err != nil {
		return err
	}
	unaligned := filepath.Join(b.tmp, "unaligned.apk")
	if err := pack(unaligned, linked, func(name string) string { return name }, files); err != nil {
		return err
	}
	aligned := filepath.Join(b.tmp, "aligned.apk")
	if err := b.tool(filepath.Join(b.buildTools, "zipalign"), "-f", "-p", "4", unaligned, aligned); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(o.out), 0o755); err != nil {
		return err
	}
	apksigner := filepath.Join(b.buildTools, "apksigner")
	if o.release() {
		return b.toolAsking(apksigner, signArgs(o.keystore, o.keyAlias, os.Getenv, o.out, aligned)...)
	}
	ks, keyErr := b.debugKey()
	if keyErr != nil {
		return keyErr
	}
	return b.tool(apksigner, "sign", "--ks", ks, "--ks-pass", "pass:android",
		"--key-pass", "pass:android", "--out", o.out, aligned)
}

// signArgs returns apksigner's arguments to sign apk into out with the
// key alias from keystore. The passwords come from $GUNIMAPK_STORE_PASS
// and $GUNIMAPK_KEY_PASS, as getenv reads them, where they are set;
// apksigner asks for the rest.
func signArgs(keystore, alias string, getenv func(string) string, out, apk string) []string {
	args := []string{"sign", "--ks", keystore, "--ks-key-alias", alias}
	if getenv("GUNIMAPK_STORE_PASS") != "" {
		args = append(args, "--ks-pass", "env:GUNIMAPK_STORE_PASS")
	}
	if getenv("GUNIMAPK_KEY_PASS") != "" {
		args = append(args, "--key-pass", "env:GUNIMAPK_KEY_PASS")
	}
	return append(args, "--out", out, apk)
}

// buildBundle writes the App Bundle o asks for: aapt2 links the
// manifest and resources as protocol buffers, link being its arguments
// after the output; they go with files into the base module, which
// bundletool makes a bundle of; and jarsigner signs it.
func (b *builder) buildBundle(o options, link []string, files []packed) error {
	bundletool, btErr := b.bundletool()
	if btErr != nil {
		return btErr
	}
	linked := filepath.Join(b.tmp, "linked.zip")
	if err := b.tool(filepath.Join(b.buildTools, "aapt2"), append([]string{"link", "--proto-format", "-o", linked}, link...)...); err != nil {
		return err
	}
	for i := range files {
		if files[i].name == "classes.dex" {
			files[i].name = "dex/classes.dex"
		}
	}
	base := filepath.Join(b.tmp, "base.zip")
	if err := pack(base, linked, moduleName, files); err != nil {
		return err
	}
	unsigned := filepath.Join(b.tmp, "unsigned.aab")
	if err := b.tool(filepath.Join(b.javaBin, "java"), "-jar", bundletool, "build-bundle",
		"--modules="+base, "--output="+unsigned); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(o.out), 0o755); err != nil {
		return err
	}
	jarsigner := filepath.Join(b.javaBin, "jarsigner")
	if o.release() {
		return b.toolAsking(jarsigner, jarsignerArgs(o.keystore, o.keyAlias, os.Getenv, o.out, unsigned)...)
	}
	ks, keyErr := b.debugKey()
	if keyErr != nil {
		return keyErr
	}
	return b.tool(jarsigner, "-keystore", ks, "-storepass", "android", "-keypass", "android",
		"-signedjar", o.out, unsigned, "androiddebugkey")
}

// moduleName returns where a file aapt2 linked goes in a bundle's
// module: the manifest under manifest/, the resources as they are, and
// anything else under root/.
func moduleName(name string) string {
	switch {
	case name == "AndroidManifest.xml":
		return "manifest/" + name
	case name == "resources.pb", strings.HasPrefix(name, "res/"):
		return name
	}
	return "root/" + name
}

// jarsignerArgs returns jarsigner's arguments to sign bundle into out
// with the key alias from keystore, the passwords taken as signArgs
// takes them.
func jarsignerArgs(keystore, alias string, getenv func(string) string, out, bundle string) []string {
	args := []string{"-keystore", keystore}
	if getenv("GUNIMAPK_STORE_PASS") != "" {
		args = append(args, "-storepass:env", "GUNIMAPK_STORE_PASS")
	}
	if getenv("GUNIMAPK_KEY_PASS") != "" {
		args = append(args, "-keypass:env", "GUNIMAPK_KEY_PASS")
	}
	return append(args, "-signedjar", out, bundle, alias)
}

// installBundle installs bundle on the device adb sees, as the APKs
// Google Play would make of it for that device, signed with the debug
// key.
func (b *builder) installBundle(bundle, adb string) error {
	bundletool, err := b.bundletool()
	if err != nil {
		return err
	}
	ks, err := b.debugKey()
	if err != nil {
		return err
	}
	apks := filepath.Join(b.tmp, "device.apks")
	javaExe := filepath.Join(b.javaBin, "java")
	if err := b.tool(javaExe, "-jar", bundletool, "build-apks", "--bundle="+bundle, "--output="+apks,
		"--connected-device", "--adb="+adb, "--ks="+ks, "--ks-pass=pass:android",
		"--ks-key-alias=androiddebugkey", "--key-pass=pass:android"); err != nil {
		return err
	}
	return b.tool(javaExe, "-jar", bundletool, "install-apks", "--apks="+apks, "--adb="+adb)
}

// bundletoolVersion is the bundletool gunimapk fetches from GitHub, and
// bundletoolSHA256 the checksum of its jar there.
const (
	bundletoolVersion = "1.18.3"
	bundletoolSHA256  = "a099cfa1543f55593bc2ed16a70a7c67fe54b1747bb7301f37fdfd6d91028e29"
)

// bundletool returns Google's bundletool, as a jar: the one
// $BUNDLETOOL names, or bundletoolVersion, fetched into the user's
// cache the first time and checked against bundletoolSHA256.
func (b *builder) bundletool() (string, error) {
	if jar := os.Getenv("BUNDLETOOL"); jar != "" {
		return jar, nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("no cache to keep bundletool in: %w; set BUNDLETOOL to its jar", err)
	}
	jar := filepath.Join(cache, "gunimapk", "bundletool-all-"+bundletoolVersion+".jar")
	if exists(jar) {
		return jar, nil
	}
	url := "https://github.com/google/bundletool/releases/download/" + bundletoolVersion +
		"/bundletool-all-" + bundletoolVersion + ".jar"
	log.Printf("fetching bundletool %s from %s", bundletoolVersion, url)
	if err := fetch(b.ctx, url, jar, bundletoolSHA256); err != nil {
		return "", fmt.Errorf("fetch bundletool: %w; set BUNDLETOOL to a jar of it", err)
	}
	return jar, nil
}

// fetch downloads url into file, if its SHA-256 is sum; a download that
// fails or differs leaves no file.
func fetch(ctx context.Context, url, file, sum string) (err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	dir := filepath.Dir(file)
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		return mkErr
	}
	part, err := os.CreateTemp(dir, filepath.Base(file)+".*")
	if err != nil {
		return err
	}
	defer func() {
		_ = part.Close()
		if err != nil {
			_ = os.Remove(part.Name())
		}
	}()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(part, h), resp.Body); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sum {
		return fmt.Errorf("%s has SHA-256 %s, want %s", url, got, sum)
	}
	if err := part.Close(); err != nil {
		return err
	}
	return os.Rename(part.Name(), file)
}

// genKey makes keystore with one key, alias, for signing release
// builds: RSA of 4096 bits, good for 10,000 days, as Google Play asks of
// a key that lasts past 2033. keytool asks for the passwords and the
// name to put in it. It stops at a keystore already there, as a key
// lost is an update path lost.
func (b *builder) genKey(keystore, alias string) error {
	if exists(keystore) {
		return fmt.Errorf("%s is there already; leave out -genkey to sign with it", keystore)
	}
	if dir := filepath.Dir(keystore); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return b.toolAsking(filepath.Join(b.javaBin, "keytool"), "-genkeypair", "-keystore", keystore,
		"-alias", alias, "-keyalg", "RSA", "-keysize", "4096", "-validity", "10000")
}

// goLib builds pkg as lib/<abi>/libgunim.so under the working directory.
func (b *builder) goLib(pkg, abi string) (string, error) {
	a, ok := abis[abi]
	if !ok {
		return "", fmt.Errorf("unknown ABI %q", abi)
	}
	host := runtime.GOOS + "-x86_64"
	cc := filepath.Join(b.ndk, "toolchains", "llvm", "prebuilt", host, "bin", fmt.Sprintf("%s%d-clang", a.clang, minSDK))
	lib := filepath.Join(b.tmp, "lib", abi, "libgunim.so")
	cmd := exec.CommandContext(b.ctx, "go", "build", "-buildmode=c-shared", "-trimpath", "-ldflags=-s -w", "-o", lib, pkg)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=1", "GOOS=android", "GOARCH="+a.goarch, "CC="+cc, "CXX="+cc+"++")
	if a.goarch == "arm" {
		cmd.Env = append(cmd.Env, "GOARM=7")
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("build %s for %s: %w", pkg, abi, err)
	}
	return lib, checkPages(lib)
}

// pageSize is the largest memory page Android runs on. Google Play
// takes only native libraries whose segments load at its boundaries.
const pageSize = 16 << 10

// checkPages reports a library whose segments would not load on a
// phone with 16 KB pages.
func checkPages(lib string) error {
	f, err := elf.Open(lib)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	for _, p := range f.Progs {
		if p.Type == elf.PT_LOAD && p.Align < pageSize {
			return fmt.Errorf("%s: a segment loads at %d-byte boundaries; phones with 16 KB pages, and Google Play, need %d",
				lib, p.Align, pageSize)
		}
	}
	return nil
}

// dex compiles the Java half of the driver into classes.dex.
func (b *builder) dex() (string, error) {
	src := filepath.Join(b.tmp, "java")
	classes := filepath.Join(b.tmp, "classes")
	dexDir := filepath.Join(b.tmp, "dex")
	for _, d := range []string{src, classes, dexDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return "", err
		}
	}
	files, globErr := fs.Glob(java.Sources, "*.java")
	if globErr != nil {
		return "", globErr
	}
	args := []string{"--release", "11", "-nowarn", "-classpath", b.androidJar, "-d", classes}
	for _, f := range files {
		data, readErr := java.Sources.ReadFile(f)
		if readErr != nil {
			return "", readErr
		}
		path := filepath.Join(src, f)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return "", err
		}
		args = append(args, path)
	}
	if err := b.tool(filepath.Join(b.javaBin, "javac"), args...); err != nil {
		return "", err
	}
	var compiled []string
	walkErr := filepath.WalkDir(classes, func(p string, d fs.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(p, ".class") {
			compiled = append(compiled, p)
		}
		return err
	})
	if walkErr != nil {
		return "", walkErr
	}
	args = append([]string{"--release", "--min-api", strconv.Itoa(minSDK), "--lib", b.androidJar, "--output", dexDir}, compiled...)
	if err := b.tool(filepath.Join(b.buildTools, "d8"), args...); err != nil {
		return "", err
	}
	return filepath.Join(dexDir, "classes.dex"), nil
}

// icon compiles the launcher's icon from a PNG, as resources for aapt2
// to link: the adaptive icon's layers, bg under the picture, and the
// plain icon.
func (b *builder) icon(png, bg string) (string, error) {
	res := filepath.Join(b.tmp, "res")
	if err := adaptiveIcon(res, png, bg); err != nil {
		return "", err
	}
	out := filepath.Join(b.tmp, "res.zip")
	return out, b.tool(filepath.Join(b.buildTools, "aapt2"), "compile", "--dir", res, "-o", out)
}

// versionOf names a build: the commit of the package's repository,
// with -dirty for edits since, and the time it was built, as
// "fd8f5fa-dirty 2026-10-03 11:30". A debug build shows it as it
// starts, so a phone says which build it runs.
func versionOf(ctx context.Context, pkg string) string {
	at := time.Now().Format("2006-01-02 15:04")
	dir, err := exec.CommandContext(ctx, "go", "list", "-f", "{{.Dir}}", pkg).Output()
	if err != nil {
		return at
	}
	commit, err := exec.CommandContext(ctx, "git", "-C", strings.TrimSpace(string(dir)), "describe", "--always", "--dirty").Output()
	if err != nil {
		return at
	}
	return strings.TrimSpace(string(commit)) + " " + at
}

// debugKey returns the debug keystore, which it makes as Android
// Studio would where there is none.
func (b *builder) debugKey() (string, error) {
	ks := home(".android", "debug.keystore")
	if exists(ks) {
		return ks, nil
	}
	if err := os.MkdirAll(filepath.Dir(ks), 0o755); err != nil {
		return "", err
	}
	err := b.tool(filepath.Join(b.javaBin, "keytool"), "-genkeypair", "-keystore", ks, "-storepass", "android",
		"-alias", "androiddebugkey", "-keypass", "android", "-keyalg", "RSA", "-keysize", "2048",
		"-validity", "10000", "-dname", "CN=Android Debug,O=Android,C=US")
	return ks, err
}

// tool runs a tool, with Java on the PATH for the tools that are Java
// programs.
func (b *builder) tool(name string, args ...string) error { return b.runTool(nil, name, args...) }

// toolAsking is tool for a tool that may ask the user something, as
// for a password, on the terminal.
func (b *builder) toolAsking(name string, args ...string) error {
	return b.runTool(os.Stdin, name, args...)
}

func (b *builder) runTool(stdin io.Reader, name string, args ...string) error {
	cmd := exec.CommandContext(b.ctx, name, args...)
	cmd.Env = append(os.Environ(), "PATH="+b.javaBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if filepath.IsAbs(b.javaBin) {
		cmd.Env = append(cmd.Env, "JAVA_HOME="+filepath.Dir(b.javaBin))
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(name), err)
	}
	return nil
}

// packed is a file to pack, by its name in the archive and its path.
type packed struct{ name, path string }

// pack writes an APK or a bundle's module: what aapt2 linked, each file
// named as rename names it, and files, as the dex and the libraries.
func pack(out, linked string, rename func(string) string, files []packed) (err error) {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	zw := zip.NewWriter(f)
	zr, err := zip.OpenReader(linked)
	if err != nil {
		return err
	}
	defer func() { _ = zr.Close() }()
	for _, e := range zr.File {
		h := e.FileHeader
		h.Name = rename(e.Name)
		w, err := zw.CreateRaw(&h)
		if err != nil {
			return err
		}
		r, err := e.OpenRaw()
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, r); err != nil {
			return err
		}
	}
	add := func(name, path string) error {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if err != nil {
			return err
		}
		r, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = r.Close() }()
		_, err = io.Copy(w, r)
		return err
	}
	for _, f := range files {
		if err := add(f.name, f.path); err != nil {
			return err
		}
	}
	return zw.Close()
}

// permissions are the manifest's lines for each permission a program
// may need, by -permissions' names. music is the one a program asks for
// with gunim's App.Ask to read the phone's music: its own permission
// since Android 13, and part of reading storage before. microphone is
// the one a program asks for with App.Ask to record, as package
// audio/speaker records through AAudio. playback lets a
// program that calls App.SetNowPlaying play on unseen, as a foreground
// service, which Google Play asks a developer to explain; a program
// without it plays while it is in front, and shows no media controls.
var permissions = map[string]string{
	"music": `	<uses-permission android:name="android.permission.READ_MEDIA_AUDIO"/>
	<uses-permission android:name="android.permission.READ_EXTERNAL_STORAGE" android:maxSdkVersion="32"/>
`,
	"microphone": `	<uses-permission android:name="android.permission.RECORD_AUDIO"/>
`,
	"playback": `	<uses-permission android:name="android.permission.FOREGROUND_SERVICE"/>
	<uses-permission android:name="android.permission.FOREGROUND_SERVICE_MEDIA_PLAYBACK"/>
`,
}

// categories are -category's names, Android's own for the kinds of app.
// Android 16 keeps a game's orientation on a large screen, where it
// lets other apps turn and resize.
var categories = []string{"accessibility", "audio", "game", "image", "maps", "news", "productivity", "social", "video"}

// orientations are -orientation's names, Android's own for the ways an
// activity's screen may be held: portrait upright alone, sensorPortrait
// upright or upside down, userPortrait as sensorPortrait while the user
// lets the screen rotate, and the same for landscape.
var orientations = []string{
	"portrait", "reversePortrait", "sensorPortrait", "userPortrait",
	"landscape", "reverseLandscape", "sensorLandscape", "userLandscape",
	"unspecified", "behind", "sensor", "fullSensor", "nosensor", "user", "fullUser", "locked",
}

// manifestFor returns the manifest of an APK that starts gunim's
// activity, as o asks: its ID and label, the launcher's icon from the
// resources where o has one, the permissions o.perms names, and the
// kind of app o.category names, where it names one; a game keeps its
// orientation on a tablet, where Android 16 lets other apps turn. The
// activity holds the screen as o.orientation names, or turns with the
// phone where it is "". It
// keeps itself across rotation and a keyboard coming and going, hears
// the back gesture through the callback GunimActivity registers, and
// slides up as the soft keyboard opens, to keep the text caret above it. With
// playback among o.perms it holds the service a program starts with
// gunim's App.SetNowPlaying, to play media on in the background. The
// provider hands the files a program shares with gunim's Client.Share to
// the application they go to, and the vibration permission, which the
// system grants as the program installs, lets it run Client.Vibrate.
func manifestFor(o options) string {
	launchMode, opensFilters := "", ""
	if len(o.opens) > 0 {
		launchMode = `
			android:launchMode="singleTask"`
		opensFilters = opensXML(o.opens)
	}
	id, name, perms, orientation := o.id, o.name, o.perms, o.orientation
	iconAttr := ""
	if o.iconPNG != "" {
		iconAttr = ` android:icon="@mipmap/ic_launcher"`
	}
	orientationAttr := ""
	if orientation != "" {
		orientationAttr = `
			android:screenOrientation="` + orientation + `"`
	}
	var asks strings.Builder
	for _, p := range perms {
		asks.WriteString(permissions[p])
	}
	categoryAttr := ""
	if o.category != "" {
		categoryAttr = ` android:appCategory="` + o.category + `"`
	}
	service := ""
	if slices.Contains(perms, "playback") {
		service = `		<service android:name="gunim.android.GunimService" android:exported="false"
			android:foregroundServiceType="mediaPlayback"/>
`
	}
	return `<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android" package="` + id + `">
	<uses-permission android:name="android.permission.VIBRATE"/>
` + asks.String() + `	<application android:label="` + xmlEscape(name) + `"` + iconAttr + categoryAttr + ` android:hasCode="true" android:extractNativeLibs="true"
		android:enableOnBackInvokedCallback="true">
		<activity android:name="gunim.android.GunimActivity" android:exported="true"
			android:configChanges="orientation|screenSize|screenLayout|smallestScreenSize|keyboard|keyboardHidden|navigation|uiMode|density"
			android:windowSoftInputMode="adjustPan|stateAlwaysHidden"` + orientationAttr + launchMode + `
			android:theme="@android:style/Theme.DeviceDefault.NoActionBar">
			<intent-filter>
				<action android:name="android.intent.action.MAIN"/>
				<category android:name="android.intent.category.LAUNCHER"/>
			</intent-filter>
` + opensFilters + `		</activity>
` + service + `		<provider android:name="gunim.android.GunimFiles" android:authorities="` + id + `.gunim.files"
			android:exported="false" android:grantUriPermissions="true"/>
	</application>
</manifest>
`
}

// extension is what -opens takes: a dot, then letters, digits and more
// dots, in lower case once parseOpens has lowered it.
var extension = regexp.MustCompile(`^(\.[a-z0-9_+-]+)+$`)

// parseOpens reads -opens' list of extensions, in lower case.
func parseOpens(list string) ([]string, error) {
	if list == "" {
		return nil, nil
	}
	var exts []string
	for _, e := range strings.Split(list, ",") {
		e = strings.ToLower(strings.TrimSpace(e))
		if !extension.MatchString(e) {
			return nil, fmt.Errorf("-opens: %q is no extension; write it as .hej", e)
		}
		if !slices.Contains(exts, e) {
			exts = append(exts, e)
		}
	}
	return exts, nil
}

// opensXML returns the activity's lines for opening exts, and names them
// for the driver, which holds back other files the MIME type lets by.
//
// The first filter matches an extension in a file's address, and needs a
// host, as Android ignores a path without one. pathSuffix does it from
// Android 12. Before that pathPattern does, whose ".*" stops at the first
// dot it finds, so it takes a pattern for each further dot in the name.
// The second takes the addresses that hold no name, as Files gives for
// Downloads, and the third files shared to the program: both have only
// the MIME type to go by.
func opensXML(exts []string) string {
	var b strings.Builder
	b.WriteString(`			<intent-filter>
				<action android:name="android.intent.action.VIEW"/>
				<category android:name="android.intent.category.DEFAULT"/>
				<data android:scheme="content" android:host="*" android:mimeType="*/*"/>
`)
	for _, e := range exts {
		escaped := strings.ReplaceAll(e, ".", `\\.`)
		fmt.Fprintf(&b, "\t\t\t\t<data android:pathSuffix=%q/>\n", e)
		for dots := range 4 {
			fmt.Fprintf(&b, "\t\t\t\t<data android:pathPattern=\"%s%s\"/>\n", strings.Repeat(`.*\\.`, dots), ".*"+escaped)
		}
	}
	b.WriteString(`			</intent-filter>
			<intent-filter>
				<action android:name="android.intent.action.VIEW"/>
				<category android:name="android.intent.category.DEFAULT"/>
				<data android:scheme="content" android:mimeType="application/octet-stream"/>
			</intent-filter>
			<intent-filter>
				<action android:name="android.intent.action.SEND"/>
				<action android:name="android.intent.action.SEND_MULTIPLE"/>
				<category android:name="android.intent.category.DEFAULT"/>
				<data android:mimeType="application/octet-stream"/>
			</intent-filter>
			<meta-data android:name="gunim.opens" android:value="` + strings.Join(exts, ",") + `"/>
`)
	return b.String()
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

// newest returns the subdirectory of dir whose name sorts last as a
// version.
func newest(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return "", fmt.Errorf("%s is empty", dir)
	}
	slices.SortFunc(names, compareVersions)
	return filepath.Join(dir, names[len(names)-1]), nil
}

// compareVersions orders names such as 29.0.14206865 and android-36 by
// the numbers in them.
func compareVersions(a, b string) int {
	num := regexp.MustCompile(`\d+`)
	x, y := num.FindAllString(a, -1), num.FindAllString(b, -1)
	for i := 0; i < len(x) && i < len(y); i++ {
		n, _ := strconv.Atoi(x[i])
		m, _ := strconv.Atoi(y[i])
		if n != m {
			return n - m
		}
	}
	return len(x) - len(y)
}

func home(parts ...string) string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(append([]string{h}, parts...)...)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
