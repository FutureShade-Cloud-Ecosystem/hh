export const MONEY_WORDS: string[];
export const MONEY_FIGURE: RegExp;
export interface FenceHit {
  file: string;
  kind: "word" | "figure";
  word: string;
  count: number;
  window: string;
}
export function withoutSpacing(text: string): string;
export function scanText(name: string, text: string): FenceHit[];
export function textFiles(root: string, extensions: string[]): Array<[string, string]>;
export function describeHits(hits: FenceHit[]): string[];
export function scanPayload(name: string, text: string): FenceHit[];
