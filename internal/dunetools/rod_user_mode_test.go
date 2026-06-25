package dunetools

import (
	"path/filepath"
	"testing"

	"github.com/go-rod/rod/lib/launcher/flags"
)

func TestRodUserModeBrowserProfileDirUsesAccountProfile(t *testing.T) {
	// Given
	browser := RodUserModeBrowser{ProfileRoot: filepath.Join("root", "dune")}
	account := Account{Email: "alice+dune@example.com"}

	// When
	got := browser.profileDir(account)

	// Then
	want := filepath.Join("root", "dune", "profiles", "rod_alice_dune_at_example_com")
	if got != want {
		t.Fatalf("profileDir = %q, want %q", got, want)
	}
}

func TestRodUserModeBrowserProfileDirCanUseDefaultChromeProfile(t *testing.T) {
	// Given
	t.Setenv("DUNE_ROD_USE_DEFAULT_PROFILE", "1")
	browser := RodUserModeBrowser{ProfileRoot: filepath.Join("root", "dune")}

	// When
	got := browser.profileDir(Account{Email: "alice@example.com"})

	// Then
	if got != "" {
		t.Fatalf("profileDir = %q, want empty default profile", got)
	}
}

func TestRodUserModeBrowserRemoteDebuggingPortUsesQueryEnv(t *testing.T) {
	// Given
	t.Setenv("DUNE_QUERY_CDP_PORT", "9222")
	browser := RodUserModeBrowser{}

	// When
	got := browser.remoteDebuggingPort()

	// Then
	if got != 9222 {
		t.Fatalf("remoteDebuggingPort = %d, want 9222", got)
	}
}

func TestRodUserModeBrowserNewUserModeLauncherUsesDetectedChromePath(t *testing.T) {
	// Given
	oldDetect := detectRodChromePath
	detectRodChromePath = func() string { return `C:\Program Files\Google\Chrome\Application\chrome.exe` }
	t.Cleanup(func() { detectRodChromePath = oldDetect })
	browser := RodUserModeBrowser{Port: 39999}

	// When
	launcher := browser.newUserModeLauncher(Account{Email: "alice@example.com"})

	// Then
	if got := launcher.Get(flags.Bin); got != `C:\Program Files\Google\Chrome\Application\chrome.exe` {
		t.Fatalf("launcher bin = %q, want Chrome path", got)
	}
}

func TestRodUserModeBrowserNewUserModeLauncherDoesNotPanicWithProfileDir(t *testing.T) {
	// Given
	browser := RodUserModeBrowser{ProfileRoot: t.TempDir(), Port: 39999}
	account := Account{Email: "alice+dune@example.com"}

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("newUserModeLauncher panicked: %v", recovered)
		}
	}()
	if launcher := browser.newUserModeLauncher(account); launcher == nil {
		t.Fatal("newUserModeLauncher returned nil")
	}
}
