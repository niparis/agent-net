# scripts/gotmpdir.sh: the Go temp folder for a script's go commands. Source it;
# it defines gotmp_default and prints nothing on its own.
#
# Go writes build and test binaries to GOTMPDIR and deletes them after each run.
# On Linux a tmpfs in RAM takes those writes when /dev/shm has room. The rule is
# the daemon's (internal/daemon/gotmp.go): /dev/shm/agent-net-gotmp when /dev/shm
# is a tmpfs with at least 4 GiB free for a user other than root, else
# /tmp/agent-net-gotmp.
#
# gotmp_default prints the folder. It does not create it.

gotmp_default() {
	gotmp_shm_min_bytes=4294967296
	if [ -d /dev/shm ]; then
		# %T is the file system type, %S the fragment size, %a the free blocks for
		# a user other than root. Each is a single word.
		gotmp_stat=$(stat -f -c '%T %S %a' /dev/shm 2>/dev/null) || gotmp_stat=
		set -- $gotmp_stat
		if [ "${1-}" = tmpfs ] && [ -n "${2-}" ] && [ -n "${3-}" ] &&
			[ $(($2 * $3)) -ge "$gotmp_shm_min_bytes" ]; then
			printf '%s\n' /dev/shm/agent-net-gotmp
			return 0
		fi
	fi
	printf '%s\n' /tmp/agent-net-gotmp
}
