import { execSync, spawnSync } from "child_process";
import { mkdtempSync, rmSync, writeFileSync } from "fs";
import { tmpdir } from "os";
import { join } from "path";

const INDEX = join(import.meta.dir, "..", "src", "index.ts");

export interface WeaveResult {
  ok: boolean;
  stdout: string;
  stderr: string;
  exitCode: number;
}

export interface Repo {
  path: string;
  git(args: string): string;
  gitSafe(args: string): { ok: boolean; stdout: string; stderr: string };
  weld(args: string[]): WeaveResult;
  commit(file: string, content: string, message: string): void;
  branch(name: string, from?: string): void;
  checkout(name: string): void;
  head(ref: string): string;
  configValue(key: string): string | null;
  cleanup(): void;
}

export function createRepo(opts: { defaultBranch?: string } = {}): Repo {
  const path = mkdtempSync(join(tmpdir(), "git-weld-test-"));
  const defaultBranch = opts.defaultBranch ?? "main";

  const repo: Repo = {
    path,
    git(args: string) {
      return execSync(`git ${args}`, {
        cwd: path,
        encoding: "utf-8",
        stdio: ["pipe", "pipe", "pipe"],
      }).trim();
    },
    gitSafe(args: string) {
      const result = spawnSync("git", args.split(/\s+/).filter(Boolean), {
        cwd: path,
        encoding: "utf-8",
      });
      return {
        ok: result.status === 0,
        stdout: (result.stdout ?? "").trim(),
        stderr: (result.stderr ?? "").trim(),
      };
    },
    weld(args: string[]) {
      const result = spawnSync("bun", ["run", INDEX, ...args], {
        cwd: path,
        encoding: "utf-8",
      });
      return {
        ok: result.status === 0,
        stdout: (result.stdout ?? "").trim(),
        stderr: (result.stderr ?? "").trim(),
        exitCode: result.status ?? 0,
      };
    },
    commit(file: string, content: string, message: string) {
      writeFileSync(join(path, file), content);
      this.git(`add ${file}`);
      this.git(`commit -m "${message}"`);
    },
    branch(name: string, from?: string) {
      if (from) this.git(`checkout ${from}`);
      this.git(`checkout -b ${name}`);
    },
    checkout(name: string) {
      this.git(`checkout ${name}`);
    },
    head(ref: string) {
      return this.git(`rev-parse ${ref}`);
    },
    configValue(key: string) {
      const r = this.gitSafe(`config --get ${key}`);
      return r.ok ? r.stdout : null;
    },
    cleanup() {
      rmSync(path, { recursive: true, force: true });
    },
  };

  repo.git(`init -q -b ${defaultBranch}`);
  repo.git("config user.email test@test.local");
  repo.git("config user.name test");
  repo.commit("README", "init", "init");

  return repo;
}
