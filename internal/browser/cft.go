package browser

import (
	"fmt"
	"path"
)

// PinnedVersion is the Chrome for Testing build that pagevow installs.
const PinnedVersion = "154.0.8037.92"

// DefaultBaseURL is where the Chrome for Testing archives are served.
const DefaultBaseURL = "https://storage.googleapis.com/chrome-for-testing-public"

// Pin describes one archive of the pinned build: its size, its SHA-256 and the executable inside it.
type Pin struct {
	Platform   string
	Size       int64
	SHA256     string
	Executable string
}

// ArchiveName returns the file name of the archive.
func (p Pin) ArchiveName() string { return "chrome-" + p.Platform + ".zip" }

// URL returns the download address of the archive below baseURL.
func (p Pin) URL(baseURL string) string {
	return baseURL + "/" + PinnedVersion + "/" + p.Platform + "/" + p.ArchiveName()
}

const macExecutable = "Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing"

func pinTable() []Pin {
	return []Pin{
		{"linux64", 196202491, "ff43322f335e436b2f4dcdfeeec5db032299e335a7e8c1c618b326e100ce8732", "chrome-linux64/chrome"},
		{"linux-arm64", 196517840, "c0af361aab66b24c72e36a4326dce4d7edf4bc23f9aede8af988c2cb6ea3fec0", "chrome-linux-arm64/chrome"},
		{"mac-arm64", 191153086, "b62e904b6571c5ff5108ed7812cf93ac6d1c4027f10ae47ac34d8e229ed88001", path.Join("chrome-mac-arm64", macExecutable)},
		{"mac-x64", 202045791, "9ddd0959603a3cdb90926427b2942279d81414c9aad856c97d405bb406d50e8b", path.Join("chrome-mac-x64", macExecutable)},
		{"win64", 205614867, "b897ef3601c947ac0620c784556dec719ac602b0159ce105927acf645ee0f598", "chrome-win64/chrome.exe"},
		{"win32", 184469809, "ed07adc92a948b215fe75d86b02ceb1b6993cad7979d2437e836aaea6b3679d4", "chrome-win32/chrome.exe"},
	}
}

// PlatformName maps an operating system and architecture to the Chrome for Testing platform name.
func PlatformName(goos, goarch string) (string, error) {
	switch goos + "/" + goarch {
	case "linux/amd64":
		return "linux64", nil
	case "linux/arm64":
		return "linux-arm64", nil
	case "darwin/arm64":
		return "mac-arm64", nil
	case "darwin/amd64":
		return "mac-x64", nil
	case "windows/amd64":
		return "win64", nil
	case "windows/386":
		return "win32", nil
	}
	return "", fmt.Errorf("no Chrome for Testing build exists for %s/%s", goos, goarch)
}

// PinFor returns the pinned archive for an operating system and architecture.
func PinFor(goos, goarch string) (Pin, error) {
	platform, err := PlatformName(goos, goarch)
	if err != nil {
		return Pin{}, err
	}
	for _, pin := range pinTable() {
		if pin.Platform == platform {
			return pin, nil
		}
	}
	return Pin{}, fmt.Errorf("no pinned Chrome for Testing archive for platform %s", platform)
}
