export interface Command {
  name: string;
  description: string;
  usage: string;
  examples?: string[];
  hidden?: boolean;
  run(args: string[]): void;
}
