export function formatTimestamp(sec: number): string {
  const n = Math.trunc(sec);
  const pair = (value: number) => String(value).padStart(2, "0");
  const minutes = pair(Math.trunc(n / 60) % 60);
  const seconds = pair(n % 60);
  return (
    (n >= 3600 ? Math.trunc(n / 3600) + ":" : "") + minutes + ":" + seconds
  );
}
