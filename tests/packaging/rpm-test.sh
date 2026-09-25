#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Agent RPM test (ADR-0006, ADR-0015). Builds dist/dbr2-agent-<VERSION>-<BUILD>
# and a BUILD+1 copy with `make rpm`, then in a throwaway container per
# distribution (systemd is not running there, which the scriptlets must
# tolerate):
#   install -> files, modes, owners, rpm -V, version output, RPM metadata,
#              systemd-analyze verify, not auto-enabled
#   upgrade -> BUILD+1 keeps the enablement and the config/state
#   remove  -> service disabled, files gone, /etc/dbr2 and /var/lib/dbr2/agent
#              (with an enrolled host's files) left in place
#
# Requires Docker. Creates only containers named dbr2test-rpm-* and removes
# them on exit. Environment: BUILD (default 0), RPM_TEST_IMAGES (space-separated
# image list overriding the defaults below).
set -euo pipefail
cd "$(dirname "$0")/../.."

# docker.io/library/rockylinux stopped at 9.3 (2024); the Rocky Linux project
# publishes current images as docker.io/rockylinux/rockylinux. Pin the minor.
DEFAULT_IMAGES="docker.io/rockylinux/rockylinux:9.8 docker.io/library/fedora:44"
read -r -a IMAGES <<<"${RPM_TEST_IMAGES:-$DEFAULT_IMAGES}"

BUILD=${BUILD:-0}
NEXT=$((BUILD + 1))
VERSION=$(tr -d '[:space:]' <cmd/agent/VERSION)
RPM=dbr2-agent-$VERSION-$BUILD.x86_64.rpm
RPM_NEXT=dbr2-agent-$VERSION-$NEXT.x86_64.rpm
PREFIX=dbr2test-rpm-$$
pass=0 fail=0
ok()  { echo "PASS: $*"; pass=$((pass + 1)); }
bad() { echo "FAIL: $*"; fail=$((fail + 1)); }
indent() { sed 's/^/    /'; }

pkgdir=$(mktemp -d)
cleanup() {
  mapfile -t cs < <(docker ps -aq --filter "name=^$PREFIX-")
  if [[ ${#cs[@]} -gt 0 ]]; then docker rm -f "${cs[@]}" >/dev/null 2>&1 || true; fi
  rm -rf "$pkgdir"
}
trap cleanup EXIT

echo "== build (BUILD=$NEXT, then BUILD=$BUILD)"
# BUILD+1 first so bin/dbr2-agent is left at the requested BUILD.
make --no-print-directory rpm BUILD="$NEXT" >/dev/null
make --no-print-directory rpm BUILD="$BUILD" >/dev/null
cp "dist/$RPM" "dist/$RPM_NEXT" "$pkgdir/"
chmod 0755 "$pkgdir"
chmod 0644 "$pkgdir"/*.rpm

# Static checks on the unit file (host side).
unit=deployments/packaging/systemd/dbr2-agent.service
if grep -qE '^(Requires|BindsTo|Requisite)=.*docker' "$unit"; then
  bad "unit must not require docker.service"
else
  ok "unit orders after docker.service without requiring it"
fi

# Expect: <name> <command> <expected-output>. The command's combined output
# must equal the expectation exactly.
expect_eq() {
  local what=$1 want=$2 got
  got=$(docker exec "$c" bash -c "$3" 2>&1) || true
  if [[ $got == "$want" ]]; then ok "$tag: $what"; else bad "$tag: $what: want [$want] got [$got]"; fi
}
# Expect the command to succeed.
expect_ok() {
  local what=$1 out
  if out=$(docker exec "$c" bash -c "$2" 2>&1); then ok "$tag: $what"; else bad "$tag: $what"; indent <<<"$out"; fi
}
# Expect the command to fail.
expect_fail() {
  local what=$1
  if docker exec "$c" bash -c "$2" >/dev/null 2>&1; then bad "$tag: $what"; else ok "$tag: $what"; fi
}
# Run a dnf transaction; fail the check if it fails or any scriptlet complains.
dnf_tx() {
  local what=$1 out
  if ! out=$(docker exec "$c" bash -c "$2" 2>&1); then
    bad "$tag: $what"; tail -20 <<<"$out" | indent; return 1
  fi
  if grep -qiE 'scriptlet failed|warning: %(post|preun|postun)|error:' <<<"$out"; then
    bad "$tag: $what (scriptlet warning)"; tail -20 <<<"$out" | indent; return 1
  fi
  ok "$tag: $what"
  last_out=$out
}

for image in "${IMAGES[@]}"; do
  tag=${image##*/}
  echo "== $image"
  c=$PREFIX-${tag//[:.]/-}
  if ! docker run -d --rm --name "$c" -v "$pkgdir:/pkg:ro,Z" "$image" sleep infinity >/dev/null; then
    bad "$tag: start container"
    continue
  fi

  # systemd (not running) for systemctl/systemd-analyze. Docs are installed
  # (container images set tsflags=nodocs) so the %doc file can be checked.
  if ! docker exec "$c" dnf -y -q install systemd >/dev/null 2>&1; then
    bad "$tag: install systemd"
    docker rm -f "$c" >/dev/null 2>&1 || true
    continue
  fi

  last_out=
  if dnf_tx "dnf install" "dnf -y --setopt=tsflags= install /pkg/$RPM"; then
    if grep -q 'dbr2-agent enroll' <<<"$last_out"; then ok "$tag: install prints the enroll next steps"; else bad "$tag: install prints the enroll next steps"; fi
  fi

  expect_eq "binary 0755 root:root" "755 root root" "stat -c '%a %U %G' /usr/bin/dbr2-agent"
  expect_eq "/etc/dbr2 0700 root:root" "700 root root" "stat -c '%a %U %G' /etc/dbr2"
  expect_eq "/var/lib/dbr2/agent 0700 root:root" "700 root root" "stat -c '%a %U %G' /var/lib/dbr2/agent"
  expect_eq "unit 0644 root:root" "644 root root" "stat -c '%a %U %G' /usr/lib/systemd/system/dbr2-agent.service"
  expect_ok "LICENSE, NOTICE and README installed" \
    "test -s /usr/share/licenses/dbr2-agent/LICENSE && test -s /usr/share/licenses/dbr2-agent/NOTICE && test -s /usr/share/doc/dbr2-agent/README.md"
  expect_ok "no default config shipped" "test ! -e /etc/dbr2/agent.yaml"
  expect_eq "rpm -V clean" "" "rpm -V dbr2-agent"
  expect_eq "rpm metadata (Version Release License URL Arch)" \
    "$VERSION $BUILD Apache-2.0 https://github.com/AxiomOperator/dbr2 x86_64" \
    "rpm -q --qf '%{VERSION} %{RELEASE} %{LICENSE} %{URL} %{ARCH}' dbr2-agent"
  expect_ok "dbr2-agent version reports $VERSION.$BUILD" \
    "dbr2-agent version | grep -qx 'dbr2-agent $VERSION.$BUILD .*'"
  expect_eq "systemd-analyze verify" "" \
    "systemd-analyze verify /usr/lib/systemd/system/dbr2-agent.service"
  expect_eq "not enabled on first install" "disabled" "systemctl is-enabled dbr2-agent.service"

  # Simulate an enrolled, enabled host.
  expect_ok "simulate enroll + enable" \
    "echo 'server: dbr2.example:8443' >/etc/dbr2/agent.yaml && echo key >/var/lib/dbr2/agent/agent.key && systemctl enable dbr2-agent.service"

  if dnf_tx "dnf upgrade to release $NEXT" "dnf -y upgrade /pkg/$RPM_NEXT"; then
    expect_eq "upgraded release" "$NEXT" "rpm -q --qf '%{RELEASE}' dbr2-agent"
    expect_eq "still enabled after upgrade" "enabled" "systemctl is-enabled dbr2-agent.service"
    expect_ok "config and state kept on upgrade" "test -s /etc/dbr2/agent.yaml && test -s /var/lib/dbr2/agent/agent.key"
    expect_eq "rpm -V clean after upgrade" "" "rpm -V dbr2-agent"
  fi

  dnf_tx "dnf remove" "dnf -y remove dbr2-agent" || true
  expect_fail "package gone" "rpm -q dbr2-agent"
  expect_ok "binary and unit removed" "test ! -e /usr/bin/dbr2-agent && test ! -e /usr/lib/systemd/system/dbr2-agent.service"
  expect_ok "service disabled on erase" "test ! -e /etc/systemd/system/multi-user.target.wants/dbr2-agent.service"
  expect_eq "/etc/dbr2 kept (0700)" "700" "stat -c '%a' /etc/dbr2"
  expect_eq "/var/lib/dbr2/agent kept (0700)" "700" "stat -c '%a' /var/lib/dbr2/agent"
  expect_ok "agent.yaml and key kept" "test -s /etc/dbr2/agent.yaml && test -s /var/lib/dbr2/agent/agent.key"

  docker rm -f "$c" >/dev/null 2>&1 || true
done

echo "== $pass passed, $fail failed"
[[ $fail -eq 0 ]]
