# GitHub SSH key (add this to your account)

Use this **public key** in GitHub so this machine can push/pull via SSH.

## 1. Copy this key (one line)

```
ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAAH/8kfhewZYG8iEsX66XwSEM4P9rxl+UNUy2yb8x8a omnicinema-hub@appbox
```

## 2. Add it to GitHub

- Go to **GitHub.com** → **Settings** → **SSH and GPG keys** (or https://github.com/settings/keys)
- Click **New SSH key**
- **Title:** e.g. `Appbox / OmniCinema Hub`
- **Key type:** Authentication Key
- **Key:** paste the line above
- Click **Add SSH key**

## 3. Push from this machine

From the repo directory:

```bash
cd /home/appbox/DCPCLOUDAPP/omnicinema-hub
git push origin main
```

The SSH config on this machine is already set to use this key for `github.com`.

---

**Note:** The private key is at `~/.ssh/id_ed25519_github`. Keep it secure; do not share it or commit it.
