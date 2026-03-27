package sandbox

// Distro defines a Linux distribution to test against.
type Distro struct {
	Name      string // display name, e.g. "ubuntu-24.04"
	Image     string // Docker image, e.g. "ubuntu:24.04"
	Service   string // docker compose service name
	Port      int    // host port mapped to container SSH (22)
	Supported bool   // true = neo should work, false = neo should reject
}

// Matrix returns all distros in the test matrix.
// Ports must match docker-compose.yml.
func Matrix() []Distro {
	return []Distro{
		// ── Supported ──
		{Name: "ubuntu-24.04", Image: "ubuntu:24.04", Service: "ubuntu-24.04", Port: 2224, Supported: true},
		{Name: "ubuntu-24.10", Image: "ubuntu:24.10", Service: "ubuntu-24.10", Port: 2225, Supported: true},
		{Name: "debian-12", Image: "debian:12", Service: "debian-12", Port: 2230, Supported: true},
		{Name: "debian-11", Image: "debian:11", Service: "debian-11", Port: 2231, Supported: true},

		// ── Unsupported (should be rejected by neo init) ──
		{Name: "ubuntu-22.04", Image: "ubuntu:22.04", Service: "ubuntu-22.04", Port: 2222, Supported: false},
		{Name: "ubuntu-20.04", Image: "ubuntu:20.04", Service: "ubuntu-20.04", Port: 2220, Supported: false},
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
