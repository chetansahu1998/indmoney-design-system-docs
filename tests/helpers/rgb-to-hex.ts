/** Convert "rgb(r, g, b)" or "rgba(r, g, b, a)" to "#RRGGBB" (uppercase). */
export function rgbToHex(input: string): string {
  const m = input.match(/rgba?\(\s*(\d+(?:\.\d+)?)[,\s]+(\d+(?:\.\d+)?)[,\s]+(\d+(?:\.\d+)?)\s*(?:[,\s]+([\d.]+))?\s*\)/);
  if (!m) {
    if (/^#[0-9a-f]{3,8}$/i.test(input)) return input.toUpperCase();
    throw new Error(`unparseable color "${input}"`);
  }
  const [r, g, b] = [m[1], m[2], m[3]].map(Number);
  const a = m[4] !== undefined ? Math.round(parseFloat(m[4]) * 255) : null;
  const hh = (n: number) => Math.round(n).toString(16).padStart(2, "0").toUpperCase();
  const out = `#${hh(r)}${hh(g)}${hh(b)}`;
  return a !== null && a < 255 ? `${out}${hh(a)}` : out;
}
