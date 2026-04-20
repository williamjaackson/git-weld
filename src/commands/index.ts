import type { Command } from "./types";

const commands: Command[] = [];

const commandMap = new Map(commands.map((c) => [c.name, c]));

export function getCommand(name: string): Command | undefined {
  return commandMap.get(name);
}

export function getAllCommands(): Command[] {
  return commands;
}
