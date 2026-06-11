package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
)

func TestCanonicalizeConfigQuota(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		data    *ConfigData
		allow   bool
		want    QuotaConfig
		wantErr string
	}{
		"quota omitted defaults to disabled": {
			data: &ConfigData{
				StorageClassConfigData: StorageClassConfigData{
					NodePathMap: []*NodePathMapData{
						{Node: NodeDefaultNonListedNodes, Paths: []string{"/var/lib/local-path"}},
					},
				},
			},
			allow: true,
			want:  QuotaConfig{Type: QuotaTypeNone},
		},
		"xfs project quota gets defaults": {
			data: &ConfigData{
				StorageClassConfigData: StorageClassConfigData{
					NodePathMap: []*NodePathMapData{
						{Node: NodeDefaultNonListedNodes, Paths: []string{"/var/lib/local-path"}},
					},
					Quota: &QuotaConfigData{Type: string(QuotaTypeXFSProject)},
				},
			},
			allow: true,
			want: QuotaConfig{
				Type:           QuotaTypeXFSProject,
				ProjectsFile:   defaultQuotaProjectsFile,
				ProjidFile:     defaultQuotaProjidFile,
				LockDir:        defaultQuotaLockDir,
				ProjectIDStart: defaultQuotaProjectIDStart,
				ProjectIDEnd:   defaultQuotaProjectIDEnd,
			},
		},
		"custom quota paths are rejected": {
			data: &ConfigData{
				StorageClassConfigData: StorageClassConfigData{
					NodePathMap: []*NodePathMapData{
						{Node: NodeDefaultNonListedNodes, Paths: []string{"/var/lib/local-path"}},
					},
					Quota: &QuotaConfigData{
						Type:           string(QuotaTypeXFSProject),
						ProjectsFile:   "/host/etc/projects",
						ProjidFile:     "/host/etc/projid",
						LockDir:        "/host/var/lib/local-path/quota-lock",
						ProjectIDStart: 2000,
						ProjectIDEnd:   3000,
					},
				},
			},
			allow:   true,
			wantErr: "custom quota metadata paths are not allowed",
		},
		"xfs project quota requires privileged helper opt in": {
			data: &ConfigData{
				StorageClassConfigData: StorageClassConfigData{
					NodePathMap: []*NodePathMapData{
						{Node: NodeDefaultNonListedNodes, Paths: []string{"/var/lib/local-path"}},
					},
					Quota: &QuotaConfigData{Type: string(QuotaTypeXFSProject)},
				},
			},
			wantErr: "xfsProject quota requires allow-privileged-xfs-project-quota",
		},
		"unknown quota type is rejected": {
			data: &ConfigData{
				StorageClassConfigData: StorageClassConfigData{
					NodePathMap: []*NodePathMapData{
						{Node: NodeDefaultNonListedNodes, Paths: []string{"/var/lib/local-path"}},
					},
					Quota: &QuotaConfigData{Type: "zfsDataset"},
				},
			},
			allow:   true,
			wantErr: `unsupported quota type "zfsDataset"`,
		},
		"project id range must be ordered": {
			data: &ConfigData{
				StorageClassConfigData: StorageClassConfigData{
					NodePathMap: []*NodePathMapData{
						{Node: NodeDefaultNonListedNodes, Paths: []string{"/var/lib/local-path"}},
					},
					Quota: &QuotaConfigData{
						Type:           string(QuotaTypeXFSProject),
						ProjectIDStart: 3000,
						ProjectIDEnd:   2000,
					},
				},
			},
			allow:   true,
			wantErr: "projectIDStart must be less than or equal to projectIDEnd",
		},
		"relative lock dir is rejected": {
			data: &ConfigData{
				StorageClassConfigData: StorageClassConfigData{
					NodePathMap: []*NodePathMapData{
						{Node: NodeDefaultNonListedNodes, Paths: []string{"/var/lib/local-path"}},
					},
					Quota: &QuotaConfigData{
						Type:    string(QuotaTypeXFSProject),
						LockDir: "relative",
					},
				},
			},
			allow:   true,
			wantErr: "lockDir must be absolute",
		},
		"quota on shared filesystem is rejected": {
			data: &ConfigData{
				StorageClassConfigData: StorageClassConfigData{
					SharedFileSystemPath: "/srv/shared",
					Quota:                &QuotaConfigData{Type: string(QuotaTypeXFSProject)},
				},
			},
			allow:   true,
			wantErr: "xfsProject quota requires nodePathMap",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg, err := canonicalizeConfig(tt.data, tt.allow)
			if tt.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErr)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.want, cfg.Quota)
		})
	}
}

func TestXFSQuotaScriptsContainSafetyGuards(t *testing.T) {
	t.Parallel()

	for name, script := range map[string]string{
		"setup":    xfsQuotaSetupScript,
		"teardown": xfsQuotaTeardownScript,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.Contains(t, script, `LOCAL_PATH_QUOTA_TYPE:-}`)
			require.Contains(t, script, "VOL_NAME must be set when quota is enabled")
			require.Contains(t, script, `quota type xfsProject requires xfs filesystem`)
			require.Contains(t, script, `projects.lock`)
			require.NotContains(t, script, `basename "$VOL_DIR"`)
			require.True(t, strings.Contains(script, "xfs_quota -x -c"))
		})
	}
}

func TestXFSQuotaSetupScriptSearchesOnlyConfiguredProjectIDRange(t *testing.T) {
	t.Parallel()

	require.Contains(t, xfsQuotaSetupScript, "candidate <= end")
	require.Contains(t, xfsQuotaSetupScript, "$1 >= start && $1 <= end")
	require.NotContains(t, xfsQuotaSetupScript, "$1 >= candidate { candidate = $1 + 1 }")
}

func TestApplyQuotaToHelperPod(t *testing.T) {
	t.Parallel()

	privileged := true
	hostPathFileOrCreate := v1.HostPathFileOrCreate
	hostPathDirectory := v1.HostPathDirectory
	hostPathDirectoryOrCreate := v1.HostPathDirectoryOrCreate

	helperPod := &v1.Pod{
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{Name: "helper-pod", Image: "quota-helper:test"},
			},
		},
	}

	err := applyQuotaToHelperPod(helperPod, QuotaConfig{
		Type:           QuotaTypeXFSProject,
		ProjectsFile:   "/etc/projects",
		ProjidFile:     "/etc/projid",
		LockDir:        defaultQuotaLockDir,
		ProjectIDStart: 2000,
		ProjectIDEnd:   3000,
	}, "xfs-quota-helper:test")

	require.NoError(t, err)
	require.Equal(t, "xfs-quota-helper:test", helperPod.Spec.Containers[0].Image)
	require.NotNil(t, helperPod.Spec.Containers[0].SecurityContext)
	require.Equal(t, &privileged, helperPod.Spec.Containers[0].SecurityContext.Privileged)
	require.Contains(t, helperPod.Spec.Containers[0].Env, v1.EnvVar{Name: envQuotaType, Value: string(QuotaTypeXFSProject)})
	require.Contains(t, helperPod.Spec.Containers[0].Env, v1.EnvVar{Name: envQuotaProjectsFile, Value: "/etc/projects"})
	require.Contains(t, helperPod.Spec.Containers[0].Env, v1.EnvVar{Name: envQuotaProjidFile, Value: "/etc/projid"})
	require.Contains(t, helperPod.Spec.Containers[0].Env, v1.EnvVar{Name: envQuotaProjectIDStart, Value: "2000"})
	require.Contains(t, helperPod.Spec.Containers[0].Env, v1.EnvVar{Name: envQuotaProjectIDEnd, Value: "3000"})
	require.Contains(t, helperPod.Spec.Containers[0].Env, v1.EnvVar{Name: envQuotaLockDir, Value: defaultQuotaLockDir})
	require.Contains(t, helperPod.Spec.Containers[0].VolumeMounts, v1.VolumeMount{Name: helperQuotaProjectsVolName, MountPath: "/etc/projects"})
	require.Contains(t, helperPod.Spec.Containers[0].VolumeMounts, v1.VolumeMount{Name: helperQuotaProjidVolName, MountPath: "/etc/projid"})
	require.Contains(t, helperPod.Spec.Containers[0].VolumeMounts, v1.VolumeMount{Name: helperQuotaDeviceVolName, MountPath: "/dev"})
	require.Contains(t, helperPod.Spec.Containers[0].VolumeMounts, v1.VolumeMount{Name: helperQuotaLockVolName, MountPath: defaultQuotaLockDir})
	require.Contains(t, helperPod.Spec.Volumes, v1.Volume{
		Name: helperQuotaProjectsVolName,
		VolumeSource: v1.VolumeSource{
			HostPath: &v1.HostPathVolumeSource{Path: "/etc/projects", Type: &hostPathFileOrCreate},
		},
	})
	require.Contains(t, helperPod.Spec.Volumes, v1.Volume{
		Name: helperQuotaProjidVolName,
		VolumeSource: v1.VolumeSource{
			HostPath: &v1.HostPathVolumeSource{Path: "/etc/projid", Type: &hostPathFileOrCreate},
		},
	})
	require.Contains(t, helperPod.Spec.Volumes, v1.Volume{
		Name: helperQuotaDeviceVolName,
		VolumeSource: v1.VolumeSource{
			HostPath: &v1.HostPathVolumeSource{Path: "/dev", Type: &hostPathDirectory},
		},
	})
	require.Contains(t, helperPod.Spec.Volumes, v1.Volume{
		Name: helperQuotaLockVolName,
		VolumeSource: v1.VolumeSource{
			HostPath: &v1.HostPathVolumeSource{Path: defaultQuotaLockDir, Type: &hostPathDirectoryOrCreate},
		},
	})
}

func TestApplyQuotaToHelperPodRejectsConflictingMount(t *testing.T) {
	t.Parallel()

	helperPod := &v1.Pod{
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  "helper-pod",
					Image: "quota-helper:test",
					VolumeMounts: []v1.VolumeMount{
						{Name: "custom", MountPath: "/etc/projects"},
					},
				},
			},
		},
	}

	err := applyQuotaToHelperPod(helperPod, QuotaConfig{
		Type:         QuotaTypeXFSProject,
		ProjectsFile: "/etc/projects",
		ProjidFile:   "/etc/projid",
		LockDir:      defaultQuotaLockDir,
	}, "xfs-quota-helper:test")

	require.Error(t, err)
	require.Contains(t, err.Error(), "quota mount path /etc/projects is already used by volume custom")
}

func TestApplyQuotaToHelperPodRequiresDedicatedHelperImage(t *testing.T) {
	t.Parallel()

	helperPod := &v1.Pod{
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{Name: "helper-pod", Image: "busybox"},
			},
		},
	}

	err := applyQuotaToHelperPod(helperPod, QuotaConfig{
		Type:         QuotaTypeXFSProject,
		ProjectsFile: "/etc/projects",
		ProjidFile:   "/etc/projid",
		LockDir:      defaultQuotaLockDir,
	}, "")

	require.Error(t, err)
	require.Contains(t, err.Error(), "xfsProject quota requires xfs-project-quota-helper-image")
}

func TestCommandAndScriptKeysForAction(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		action          ActionType
		setupCommand    string
		teardownCommand string
		quota           QuotaConfig
		wantCommand     []string
		wantKeys        []string
	}{
		"create without quota uses setup": {
			action:      ActionTypeCreate,
			wantCommand: []string{"/bin/sh", "/script/setup"},
			wantKeys:    []string{"setup"},
		},
		"delete without quota uses teardown": {
			action:      ActionTypeDelete,
			wantCommand: []string{"/bin/sh", "/script/teardown"},
			wantKeys:    []string{"teardown"},
		},
		"create with quota uses xfs setup": {
			action:      ActionTypeCreate,
			quota:       QuotaConfig{Type: QuotaTypeXFSProject},
			wantCommand: []string{"/bin/sh", "-ec", xfsQuotaSetupScript},
		},
		"delete with quota uses xfs teardown": {
			action:      ActionTypeDelete,
			quota:       QuotaConfig{Type: QuotaTypeXFSProject},
			wantCommand: []string{"/bin/sh", "-ec", xfsQuotaTeardownScript},
		},
		"custom setup command wins without quota": {
			action:       ActionTypeCreate,
			setupCommand: "/manager",
			wantCommand:  []string{"/manager"},
		},
		"custom setup command is ignored with quota": {
			action:       ActionTypeCreate,
			setupCommand: "/manager",
			quota:        QuotaConfig{Type: QuotaTypeXFSProject},
			wantCommand:  []string{"/bin/sh", "-ec", xfsQuotaSetupScript},
		},
		"custom teardown command wins without quota": {
			action:          ActionTypeDelete,
			teardownCommand: "/manager",
			wantCommand:     []string{"/manager"},
		},
		"custom teardown command is ignored with quota": {
			action:          ActionTypeDelete,
			teardownCommand: "/manager",
			quota:           QuotaConfig{Type: QuotaTypeXFSProject},
			wantCommand:     []string{"/bin/sh", "-ec", xfsQuotaTeardownScript},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cmd, keys := commandAndScriptKeysForAction(tt.action, tt.setupCommand, tt.teardownCommand, tt.quota)
			require.Equal(t, tt.wantCommand, cmd)
			require.Equal(t, tt.wantKeys, keys)
		})
	}
}
