# Scripts

| Script | Purpose |
|---|---|
| `remote-setup.sh` | Clone repo via HTTPS and run `make cluster-up` + `make deploy` on hosts without GitHub SSH keys |

Usage:

```bash
./scripts/remote-setup.sh              # installs to ~/kube-pod-self-healer
./scripts/remote-setup.sh /opt/demo    # custom install path
```

Or from the Makefile: `make remote-setup`
