package cloud

import (
	"fmt"
	"strings"
)

// DockerMajor is the Docker Engine release a new host gets: the newest patch of this major, from
// Docker's own apt repository.
const DockerMajor = "28"

// Init is what a new host's cloud-init needs.
type Init struct {
	// Hostname is the VM's, and its name on the tailnet.
	Hostname string
	// HostKeyPrivate and HostKeyPublic are the ssh host key berth made, so it can pin it before the
	// first connection (no trust on first use).
	HostKeyPrivate, HostKeyPublic string
	// AuthorizedKey is the public key the ops user accepts: berth's, for the first login.
	AuthorizedKey string
	// TailscaleAuthKey is a single-use, tagged auth key. It is in the user data, which the VM can
	// read back from its metadata service: single-use is what makes that acceptable.
	TailscaleAuthKey string
}

func indent(s, pad string) string {
	return pad + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n"+pad)
}

// UserData is the cloud-init document: the pinned host key, an ops user for berth, password logins
// off, a pinned Docker, and Tailscale as the only way in.
func (i Init) UserData() string {
	return fmt.Sprintf(`#cloud-config
# Written by berth (host create). The provider's firewall lets nothing in: the host is reached over
# Tailscale only.
hostname: %[1]s
ssh_pwauth: false
disable_root: true
ssh_deletekeys: true
ssh_keys:
  ed25519_private: |
%[2]s
  ed25519_public: %[3]s
users:
  - name: ops
    shell: /bin/bash
    lock_passwd: true
    ssh_authorized_keys:
      - %[4]s
write_files:
  - path: /run/berth/tailscale-authkey
    permissions: "0600"
    content: %[5]s
runcmd:
  - [sh, -c, "install -m 0755 -d /etc/apt/keyrings && curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc"]
  - [sh, -c, "echo \"deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo $VERSION_CODENAME) stable\" > /etc/apt/sources.list.d/docker.list"]
  - [sh, -c, "apt-get update -q && v=$(apt-cache madison docker-ce | awk '{print $3}' | grep -m1 '^5:%[6]s\\.') && DEBIAN_FRONTEND=noninteractive apt-get install -y -q docker-ce=$v docker-ce-cli=$v containerd.io docker-compose-plugin git"]
  - [usermod, -aG, docker, ops]
  - [sh, -c, "curl -fsSL https://tailscale.com/install.sh | sh"]
  - [sh, -c, "tailscale up --auth-key=file:/run/berth/tailscale-authkey --hostname=%[1]s; rm -f /run/berth/tailscale-authkey"]
`, i.Hostname, indent(i.HostKeyPrivate, "    "), i.HostKeyPublic, i.AuthorizedKey, i.TailscaleAuthKey, DockerMajor)
}
