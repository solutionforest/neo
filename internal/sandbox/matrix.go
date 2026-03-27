package sandbox

// Distro defines a Linux distribution to test against.
type Distro struct {
	Name      string // display name, e.g. "ubuntu-24.04"
	Image     string // Docker image, e.g. "ubuntu:24.04"
	Service   string // docker compose service name
	Port      int    // host port mapped to container SSH (22)
	Supported bool   // true = neo should work, false = neo should reject
	PkgMgr   string // "apt" or "dnf" — affects Dockerfile variant
}

// Matrix returns all distros in the test matrix.
// Ports must match docker-compose.yml.
func Matrix() []Distro {
	return []Distro{
		// ── Supported: Debian/Ubuntu ──
		{Name: "ubuntu-24.04", Image: "ubuntu:24.04", Service: "ubuntu-24.04", Port: 2224, Supported: true, PkgMgr: "apt"},
		{Name: "ubuntu-24.10", Image: "ubuntu:24.10", Service: "ubuntu-24.10", Port: 2225, Supported: true, PkgMgr: "apt"},
		{Name: "debian-12", Image: "debian:12", Service: "debian-12", Port: 2230, Supported: true, PkgMgr: "apt"},
		{Name: "debian-11", Image: "debian:11", Service: "debian-11", Port: 2231, Supported: true, PkgMgr: "apt"},

		// ── Supported: RPM-based ──
		{Name: "fedora-41", Image: "fedora:41", Service: "fedora-41", Port: 2240, Supported: true, PkgMgr: "dnf"},
		{Name: "fedora-40", Image: "fedora:40", Service: "fedora-40", Port: 2241, Supported: true, PkgMgr: "dnf"},
		{Name: "centos-stream-9", Image: "quay.io/centos/centos:stream9", Service: "centos-stream-9", Port: 2250, Supported: true, PkgMgr: "dnf"},
		{Name: "almalinux-9", Image: "almalinux:9", Service: "almalinux-9", Port: 2251, Supported: true, PkgMgr: "dnf"},
		{Name: "rocky-9", Image: "rockylinux:9", Service: "rocky-9", Port: 2252, Supported: true, PkgMgr: "dnf"},

		// ── Unsupported (should be rejected by neo init) ──
		{Name: "ubuntu-22.04", Image: "ubuntu:22.04", Service: "ubuntu-22.04", Port: 2222, Supported: false, PkgMgr: "apt"},
		{Name: "ubuntu-20.04", Image: "ubuntu:20.04", Service: "ubuntu-20.04", Port: 2220, Supported: false, PkgMgr: "apt"},
		{Name: "centos-7", Image: "centos:7", Service: "centos-7", Port: 2253, Supported: false, PkgMgr: "yum"},
		{Name: "fedora-38", Image: "fedora:38", Service: "fedora-38", Port: 2242, Supported: false, PkgMgr: "dnf"},
	}
}

// SupportedOnly returns only the supported distros.
func SupportedOnly() []Distro {
	var out []Distro
	for _, d := range Matrix() {
		if d.Supported {
			out = append(out, d)
		}
	}
	return out
}

// UnsupportedOnly returns only the unsupported distros.
func UnsupportedOnly() []Distro {
	var out []Distro
	for _, d := range Matrix() {
		if !d.Supported {
			out = append(out, d)
		}
	}
	return out
}
