# XFS Project Quota Example

This example enables hard PVC size enforcement with XFS project quotas.

Requirements:

- The configured `nodePathMap` path is on an XFS filesystem mounted with `prjquota` or `pquota`.
- The helper image is built with `docker build -t local-path-xfs-quota-helper:e2e examples/quota` and contains `xfs_quota`.
- The provisioner config has `"quota": {"type": "xfsProject"}` for the storage class.
- Volume expansion is not supported for quota-enabled storage classes. Leave `allowVolumeExpansion` unset or `false`.

Quota helper privileges and host mounts are injected by the provisioner when quota is enabled. Do not set `ALLOW_UNSAFE_HELPER_POD_TEMPLATE=true` for this example.

To test manually:

```bash
docker build -t local-path-xfs-quota-helper:e2e examples/quota
kubectl apply -k examples/quota
kubectl apply -f examples/pvc/pvc.yaml
kubectl apply -f examples/pod/pod.yaml
kubectl exec volume-test -- sh -c 'dd if=/dev/zero of=/data/over-quota bs=1M count=3000'
```

The `dd` command should fail once it exceeds the PVC requested storage size.
