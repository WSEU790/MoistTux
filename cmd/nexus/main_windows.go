//go:build windows

// Command nexus is the NEXUS desktop application for Windows 10/11. It runs
// the core in-process and renders the console in a Microsoft Edge WebView2
// window. The console is only reachable on the loopback interface with the
// session token created at launch.
package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/exec"
	"syscall"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"

	"github.com/WSEU790/MoistTux/internal/core/app"
)

func messageBox(title, text string, flags uint32) {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(text)
	windows.MessageBox(0, m, t, flags)
}

func main() {
	dataDir := flag.String("data", app.DefaultDataDir(), "data directory")
	headless := flag.Bool("headless", false, "run the core without a window and open the console in the default browser")
	flag.Parse()

	unlock, err := app.Lock(*dataDir)
	if err != nil {
		messageBox("NEXUS", err.Error(), windows.MB_ICONINFORMATION)
		os.Exit(1)
	}

	var w webview2.WebView
	quitCh := make(chan struct{})
	quit := func() {
		if w != nil {
			w.Dispatch(func() { w.Terminate() })
			return
		}
		close(quitCh)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, err := app.Start(ctx, app.Options{DataDir: *dataDir, Listen: "127.0.0.1:0", Quit: quit, InstallOnStart: true})
	if errors.Is(err, app.ErrUpdating) {
		// The installer was launched; release the lock so it can replace us.
		unlock()
		os.Exit(0)
	}
	if err != nil {
		unlock()
		messageBox("NEXUS failed to start", err.Error(), windows.MB_ICONERROR)
		os.Exit(1)
	}
	defer unlock()
	defer a.Close()

	if !*headless {
		w = webview2.NewWithOptions(webview2.WebViewOptions{
			Debug:     false,
			AutoFocus: true,
			DataPath:  *dataDir + `\webview`,
			WindowOptions: webview2.WindowOptions{
				Title:  "NEXUS — Infrastructure Command Center",
				Width:  1480,
				Height: 920,
				IconId: 1, // the application icon embedded by go-winres
				Center: true,
			},
		})
		if w != nil {
			w.SetSize(1180, 720, webview2.HintMin)
			w.Navigate(a.URL)
			w.Run()
			w.Destroy()
			return
		}
		messageBox("NEXUS", "The Microsoft Edge WebView2 Runtime was not found, so NEXUS will open in your default browser instead.\n\nInstall WebView2 from https://developer.microsoft.com/microsoft-edge/webview2/ for the desktop window.", windows.MB_ICONWARNING)
	}
	openBrowser(a.URL)
	<-quitCh
}

func openBrowser(u string) {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = cmd.Start()
}
