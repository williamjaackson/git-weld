import { getCommand } from "./commands";
import { err, out } from "./core/log";

function main() {
  const args = process.argv.slice(2);
  const first = args[0];

  if (!first) {
    out("usage: git weld <command>");
    process.exit(1);
  }

  const command = getCommand(first);
  if (!command) {
    err(`unknown command: ${first}`);
    process.exit(1);
  }

  command.run(args.slice(1));
}

main();
