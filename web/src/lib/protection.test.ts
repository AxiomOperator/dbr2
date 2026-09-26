// SPDX-License-Identifier: Apache-2.0
import { describe, expect, it } from "vitest";
import { AnalysisSchema } from "@/lib/api/fleet-schemas";
import {
  componentDetail,
  componentKind,
  describeWindow,
  hhmmToMinutes,
  hookTargets,
  isValidTimeZone,
  joinCommand,
  minutesToHHMM,
  splitCommand,
} from "@/lib/protection";

describe("minutes of the day <-> HH:MM", () => {
  it("formats minutes as zero-padded HH:MM", () => {
    expect(minutesToHHMM(0)).toBe("00:00");
    expect(minutesToHHMM(90)).toBe("01:30");
    expect(minutesToHHMM(1320)).toBe("22:00");
    expect(minutesToHHMM(1439)).toBe("23:59");
    expect(minutesToHHMM(null)).toBe("");
    expect(minutesToHHMM(1440)).toBe("");
    expect(minutesToHHMM(-1)).toBe("");
  });

  it("parses HH:MM (and time-input seconds) and rejects invalid times", () => {
    expect(hhmmToMinutes("00:00")).toBe(0);
    expect(hhmmToMinutes("5:30")).toBe(330);
    expect(hhmmToMinutes("22:00:00")).toBe(1320);
    expect(hhmmToMinutes(" 23:59 ")).toBe(1439);
    for (const bad of ["", "24:00", "12:60", "12", "ab:cd", "1:2"]) expect(hhmmToMinutes(bad)).toBeNull();
  });

  it("round-trips every minute of the day", () => {
    for (let m = 0; m < 1440; m += 7) expect(hhmmToMinutes(minutesToHHMM(m))).toBe(m);
  });

  it("describes windows, including ones that wrap midnight", () => {
    expect(describeWindow(null, null)).toMatch(/No window/);
    expect(describeWindow(60, 300)).toBe("01:00–05:00");
    expect(describeWindow(1320, 330)).toBe("22:00–05:30 (overnight, wraps midnight)");
  });
});

describe("splitCommand", () => {
  it("splits on whitespace and honours quotes and escapes", () => {
    expect(splitCommand("pg_dump -U shop  shop")).toEqual(["pg_dump", "-U", "shop", "shop"]);
    expect(splitCommand(`sh -c 'psql -U shop -c "CHECKPOINT"'`)).toEqual(["sh", "-c", 'psql -U shop -c "CHECKPOINT"']);
    expect(splitCommand(`echo "a \\"b\\" c" d\\ e`)).toEqual(["echo", 'a "b" c', "d e"]);
    expect(splitCommand(`redis-cli ''`)).toEqual(["redis-cli", ""]);
    expect(splitCommand("   ")).toEqual([]);
  });

  it("does not expand anything", () => {
    expect(splitCommand("echo $HOME *.txt ~")).toEqual(["echo", "$HOME", "*.txt", "~"]);
  });

  it("rejects unterminated quotes and a trailing backslash", () => {
    expect(() => splitCommand(`sh -c 'oops`)).toThrow(/Unterminated single quote/);
    expect(() => splitCommand(`echo "oops`)).toThrow(/Unterminated double quote/);
    expect(() => splitCommand("echo \\")).toThrow(/backslash/);
  });

  it("round-trips through joinCommand", () => {
    const argvs = [
      ["sh", "-c", 'psql -U shop -c "CHECKPOINT"'],
      ["redis-cli", "BGSAVE"],
      ["echo", "it's", ""],
      ["/usr/bin/mysqladmin", "--user=root", "flush-tables"],
    ];
    for (const argv of argvs) expect(splitCommand(joinCommand(argv))).toEqual(argv);
    expect(joinCommand(["redis-cli", "BGSAVE"])).toBe("redis-cli BGSAVE");
  });
});

describe("component names", () => {
  const analysis = AnalysisSchema.parse({
    key: "compose:shop",
    kind: "compose",
    name: "shop",
    working_dir: "/srv/shop",
    source: "original",
    services: [{ name: "db", image: "postgres", containers: [{ id: "c1", name: "shop-db-1", state: "running" }] }],
    volumes: [
      { name: "shop_pgdata", driver: "local", mountpoint: "/var/lib/docker/volumes/shop_pgdata/_data", class: "local", used_by: [], protected_by_default: true },
      { name: "shop_media", driver: "local", mountpoint: "/mnt/nfs", class: "external", used_by: [], protected_by_default: false },
    ],
    bind_mounts: [
      { container: "web", source: "/srv/shop/uploads", destination: "/app/uploads", rw: true },
      { container: "worker", source: "/srv/shop/uploads", destination: "/uploads", rw: false },
      { container: "web", source: "/var/run/docker.sock", destination: "/var/run/docker.sock", rw: true },
      { container: "web", source: "/etc/localtime", destination: "/etc/localtime", rw: false },
    ],
    secrets_count: 0,
    containers: ["shop-db-1", "shop-web-1"],
  });

  it("derives the kind from the name", () => {
    expect(["config", "volume:shop_pgdata", "bind:/srv/shop/uploads", "database:db"].map(componentKind)).toEqual([
      "config",
      "volume",
      "bind_mount",
      "database",
    ]);
  });

  it("finds where a component's data lives in the analysis", () => {
    expect(componentDetail(analysis, "config")).toBe("/srv/shop");
    expect(componentDetail(analysis, "volume:shop_pgdata")).toBe("/var/lib/docker/volumes/shop_pgdata/_data");
    expect(componentDetail(analysis, "bind:/srv/shop/uploads")).toBe("/srv/shop/uploads");
    expect(componentDetail(analysis, "volume:gone")).toBeNull();
    expect(componentDetail(null, "config")).toBe("Application definition and metadata");
  });

  it("lists hook targets (services and containers)", () => {
    expect(hookTargets(analysis)).toEqual(["db", "shop-db-1", "shop-web-1"]);
  });
});

describe("isValidTimeZone", () => {
  it("accepts IANA zones and rejects garbage", () => {
    expect(isValidTimeZone("America/Chicago")).toBe(true);
    expect(isValidTimeZone("UTC")).toBe(true);
    expect(isValidTimeZone("Mars/Olympus")).toBe(false);
    expect(isValidTimeZone("")).toBe(false);
  });
});
