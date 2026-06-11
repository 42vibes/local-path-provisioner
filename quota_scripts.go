package main

const xfsQuotaSetupScript = `#!/bin/sh
set -eu

require_command() {
    command -v "$1" >/dev/null 2>&1 || {
        echo "required command not found: $1" >&2
        exit 1
    }
}

require_command awk
require_command dirname
require_command mkdir
require_command rmdir
require_command sed
require_command sleep
require_command stat
require_command xfs_quota

if [ "${LOCAL_PATH_QUOTA_TYPE:-}" != "xfsProject" ]; then
    mkdir -m 0777 -p "$VOL_DIR"
    exit 0
fi

if [ -z "${VOL_NAME:-}" ]; then
    echo "VOL_NAME must be set when quota is enabled" >&2
    exit 1
fi

if [ "${VOL_SIZE_BYTES:-0}" -le 0 ]; then
    echo "VOL_SIZE_BYTES must be greater than 0 when quota is enabled" >&2
    exit 1
fi

xfs_path=$(dirname "$VOL_DIR")
project_name=$(printf "%s" "$VOL_NAME" | sed 's/[^A-Za-z0-9_.-]/_/g')
projects_file="${LOCAL_PATH_QUOTA_PROJECTS_FILE:-/etc/projects}"
projid_file="${LOCAL_PATH_QUOTA_PROJID_FILE:-/etc/projid}"
lock_dir="${LOCAL_PATH_QUOTA_LOCK_DIR:-/var/lib/local-path-provisioner/quota-lock}"
project_id_start="${LOCAL_PATH_QUOTA_PROJECT_ID_START:-1048576}"
project_id_end="${LOCAL_PATH_QUOTA_PROJECT_ID_END:-2147483647}"

fs_type=$(stat -f -c %T "$xfs_path")
if [ "$fs_type" != "xfs" ]; then
    echo "quota type xfsProject requires xfs filesystem at $xfs_path, got $fs_type" >&2
    exit 1
fi

mkdir -m 0777 -p "$VOL_DIR"
mkdir -p "$lock_dir"

lock_path="$lock_dir/projects.lock"
while ! mkdir "$lock_path" 2>/dev/null; do
    sleep 1
done
trap 'rmdir "$lock_path"' EXIT

existing_id=$(awk -F: -v path="$VOL_DIR" '$2 == path { print $1; exit }' "$projects_file" 2>/dev/null || true)
if [ -n "$existing_id" ]; then
    project_id="$existing_id"
else
    project_id=$(awk -F: -v start="$project_id_start" -v end="$project_id_end" '
        $1 ~ /^[0-9]+$/ && $1 >= start && $1 <= end { used[$1] = 1 }
        END {
            for (candidate = start; candidate <= end; candidate++) {
                if (!(candidate in used)) {
                    print candidate
                    exit 0
                }
            }
            exit 2
        }
    ' "$projects_file") || {
        echo "no available XFS project id in range $project_id_start-$project_id_end" >&2
        exit 1
    }
    printf "%s:%s\n" "$project_id" "$VOL_DIR" >> "$projects_file"
fi

if ! awk -F: -v name="$project_name" '$1 == name { found = 1 } END { exit found ? 0 : 1 }' "$projid_file" 2>/dev/null; then
    printf "%s:%s\n" "$project_name" "$project_id" >> "$projid_file"
fi

xfs_quota -x -c "project -s $project_name" "$xfs_path"
xfs_quota -x -c "limit -p bhard=$VOL_SIZE_BYTES $project_name" "$xfs_path"
`

const xfsQuotaTeardownScript = `#!/bin/sh
set -eu

require_command() {
    command -v "$1" >/dev/null 2>&1 || {
        echo "required command not found: $1" >&2
        exit 1
    }
}

require_command awk
require_command cat
require_command dirname
require_command mkdir
require_command rm
require_command rmdir
require_command sed
require_command sleep
require_command stat
require_command xfs_quota

if [ "${LOCAL_PATH_QUOTA_TYPE:-}" != "xfsProject" ]; then
    rm -rf "$VOL_DIR"
    exit 0
fi

if [ -z "${VOL_NAME:-}" ]; then
    echo "VOL_NAME must be set when quota is enabled" >&2
    exit 1
fi

xfs_path=$(dirname "$VOL_DIR")
project_name=$(printf "%s" "$VOL_NAME" | sed 's/[^A-Za-z0-9_.-]/_/g')
projects_file="${LOCAL_PATH_QUOTA_PROJECTS_FILE:-/etc/projects}"
projid_file="${LOCAL_PATH_QUOTA_PROJID_FILE:-/etc/projid}"
lock_dir="${LOCAL_PATH_QUOTA_LOCK_DIR:-/var/lib/local-path-provisioner/quota-lock}"

fs_type=$(stat -f -c %T "$xfs_path")
if [ "$fs_type" != "xfs" ]; then
    echo "quota type xfsProject requires xfs filesystem at $xfs_path, got $fs_type" >&2
    exit 1
fi

xfs_quota -x -c "limit -p bhard=0 $project_name" "$xfs_path"
rm -rf "$VOL_DIR"
mkdir -p "$lock_dir"

lock_path="$lock_dir/projects.lock"
while ! mkdir "$lock_path" 2>/dev/null; do
    sleep 1
done
trap 'rmdir "$lock_path"' EXIT

tmp_projects="${projects_file}.local-path.$$"
tmp_projid="${projid_file}.local-path.$$"
awk -F: -v path="$VOL_DIR" '$2 != path' "$projects_file" > "$tmp_projects"
awk -F: -v name="$project_name" '$1 != name' "$projid_file" > "$tmp_projid"
cat "$tmp_projects" > "$projects_file"
cat "$tmp_projid" > "$projid_file"
rm -f "$tmp_projects" "$tmp_projid"
`
