# scripts/gotmpdir.sh: the temp folders for a script's go commands. Source it; it
# defines gotmp_default and tmp_default and prints nothing on its own.
#
# Go writes build and test binaries to GOTMPDIR and deletes them after each run,
# and the tests write their own temp files to TMPDIR. On Linux a tmpfs in RAM
# takes those writes when /dev/shm has room. The rule is the daemon's
# (internal/daemon/gotmp.go): a folder under /dev/shm when /dev/shm is a tmpfs
# with at least 4 GiB free for a user other than root.
#
# A sandbox can hide the host's /dev/shm folder, or make /dev/shm read-only, so
# a RAM folder is used only when it can be made and written here. GOTMP_SHM_ROOT
# replaces /dev/shm in tests. The OS sandbox tests copy the test binary out of
# /dev/shm before bubblewrap runs it, so these folders do not depend on bwrap.

# gotmp_ram_folder NAME prints /dev/shm/NAME when /dev/shm has room and NAME can be
# made and written there. Otherwise it prints nothing and fails.
gotmp_ram_folder() {
	gotmp_name=$1
	gotmp_shm_min_bytes=4294967296
	gotmp_shm_root=${GOTMP_SHM_ROOT:-/dev/shm}
	if [ -d "$gotmp_shm_root" ]; then
		# %T is the file system type, %S the fragment size, %a the free blocks for
		# a user other than root. Each is a single word.
		gotmp_stat=$(stat -f -c '%T %S %a' "$gotmp_shm_root" 2>/dev/null) || gotmp_stat=
		set -- $gotmp_stat
		if [ "${1-}" = tmpfs ] && [ -n "${2-}" ] && [ -n "${3-}" ] &&
			[ $(($2 * $3)) -ge "$gotmp_shm_min_bytes" ] &&
			mkdir -p "$gotmp_shm_root/$gotmp_name" 2>/dev/null &&
			[ -w "$gotmp_shm_root/$gotmp_name" ]; then
			printf '%s\n' "$gotmp_shm_root/$gotmp_name"
			return 0
		fi
	fi
	return 1
}

# gotmp_default prints the Go temp folder: the RAM folder when it is usable, else
# /tmp/agent-net-gotmp.
gotmp_default() {
	gotmp_ram_folder agent-net-gotmp || printf '%s\n' /tmp/agent-net-gotmp
}

# tmp_default prints the RAM folder for TMPDIR, and fails when there is none, so
# the caller leaves TMPDIR as it is.
tmp_default() {
	gotmp_ram_folder agent-net-tmp
}
