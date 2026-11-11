// Command nexus-winres writes the Windows resources (icon, version info and
// application manifest) for NEXUS.exe as a .syso object that `go build`
// links automatically.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"regexp"

	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"
)

func main() {
	ver := flag.String("version", "0.0.0", "product version (non-numeric suffixes are dropped)")
	iconPath := flag.String("icon", "installer/assets/icon-256.png", "256×256 PNG icon")
	out := flag.String("out", "cmd/nexus/rsrc_windows_amd64.syso", "output .syso")
	flag.Parse()

	f, err := os.Open(*iconPath)
	if err != nil {
		fail(err)
	}
	img, err := png.Decode(f)
	f.Close()
	if err != nil {
		fail(err)
	}
	icon, err := winres.NewIconFromResizedImage(img, []int{16, 20, 24, 32, 40, 48, 64, 128, 256})
	if err != nil {
		fail(err)
	}
	var _ image.Image = img

	rs := &winres.ResourceSet{}
	if err := rs.SetIcon(winres.ID(1), icon); err != nil {
		fail(err)
	}
	rs.SetManifest(winres.AppManifest{
		Identity:            winres.AssemblyIdentity{Name: "NEXUS.InfrastructureCommandCenter"},
		Description:         "NEXUS Infrastructure Command Center",
		Compatibility:       winres.Win10AndAbove,
		ExecutionLevel:      winres.AsInvoker,
		DPIAwareness:        winres.DPIPerMonitorV2,
		LongPathAware:       true,
		UseCommonControlsV6: true,
	})
	num := numeric(*ver)
	vi := version.Info{}
	vi.SetFileVersion(num)
	vi.SetProductVersion(num)
	for k, v := range map[string]string{
		version.ProductName: "NEXUS", version.FileDescription: "NEXUS Infrastructure Command Center",
		version.CompanyName: "NEXUS", version.InternalName: "NEXUS", version.OriginalFilename: "NEXUS.exe",
		version.ProductVersion: *ver, version.FileVersion: *ver, version.LegalCopyright: "NEXUS project",
	} {
		if err := vi.Set(version.LangDefault, k, v); err != nil {
			fail(err)
		}
	}
	rs.SetVersionInfo(vi)
	o, err := os.Create(*out)
	if err != nil {
		fail(err)
	}
	if err := rs.WriteObject(o, winres.ArchAMD64); err != nil {
		fail(err)
	}
	if err := o.Close(); err != nil {
		fail(err)
	}
	fmt.Println("wrote", *out, "version", num)
}

func numeric(v string) string {
	m := regexp.MustCompile(`^v?(\d+)(?:\.(\d+))?(?:\.(\d+))?(?:\.(\d+))?`).FindStringSubmatch(v)
	parts := []string{"0", "0", "0", "0"}
	for i := 1; m != nil && i <= 4; i++ {
		if m[i] != "" {
			parts[i-1] = m[i]
		}
	}
	return parts[0] + "." + parts[1] + "." + parts[2] + "." + parts[3]
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "nexus-winres:", err)
	os.Exit(1)
}
