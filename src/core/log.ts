/**
 * All CLI output goes through this module so colour and stream choice stay
 * consistent. We bypass `console.log` / `console.error` because Bun's
 * implementation wraps `console.error` in red ANSI escape codes
 * unconditionally, which is misleading for status messages that aren't
 * errors (e.g. "installed hook").
 *
 * Colour is only emitted when the target stream is a TTY — piped/redirected
 * output stays plain so scripts capturing it get clean bytes.
 */
const GRAY = "\x1b[90m";
const RED = "\x1b[31m";
const RESET = "\x1b[0m";
const stderrColour = process.stderr.isTTY;

/** Primary CLI output (IDs, names, JSON). stdout, never coloured. */
export function out(msg: string): void {
  process.stdout.write(`${msg}\n`);
}

/** Secondary status (e.g. hook installation notices). stderr, gray on TTY. */
export function info(msg: string): void {
  if (stderrColour) process.stderr.write(`${GRAY}${msg}${RESET}\n`);
  else process.stderr.write(`${msg}\n`);
}

/** Real errors. stderr, red on TTY. */
export function err(msg: string): void {
  if (stderrColour) process.stderr.write(`${RED}${msg}${RESET}\n`);
  else process.stderr.write(`${msg}\n`);
}
