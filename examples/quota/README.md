# XFS Project Quota Example

This example enables hard PVC size enforcement with XFS project quotas.

Requirements:

- The configured `nodePathMap` path is on an XFS filesystem mounted with `prjquota` or `pquota`.
- The helper image is built from the current Alpine stable release and contains `xfs_quota`.
- The provisioner config has `"quota": {"type": "xfsProject"}` for the storage class.
- The provisioner deployment opts in with `--allow-privileged-xfs-project-quota`.
- The provisioner deployment sets `--xfs-project-quota-helper-image` to the trusted quota helper image.
- Volume expansion is not supported for quota-enabled storage classes. Leave `allowVolumeExpansion` unset or `false`.

Quota helper privileges and host mounts are injected by the provisioner only after the deployment-level opt-in is enabled. The quota scripts are embedded in the provisioner binary and are not loaded from the ConfigMap. Do not set `ALLOW_UNSAFE_HELPER_POD_TEMPLATE=true` for this example.

To test manually:

```bash
docker build -t ghcr.io/42vibes/local-path-xfs-quota-helper:xfs-project-quotas examples/quota
kubectl apply -k examples/quota
kubectl apply -f examples/pvc/pvc.yaml
kubectl apply -f examples/pod/pod.yaml
kubectl exec volume-test -- sh -c 'dd if=/dev/zero of=/data/over-quota bs=1M count=3000'
```

The `dd` command should fail once it exceeds the PVC requested storage size.
