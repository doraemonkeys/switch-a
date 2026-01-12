/** Convert string to URL-friendly slug (e.g., "OpenAI Production" → "openai-production") */
export function slugify(str: string): string {
  return str
    .toLowerCase()
    .trim()
    .replace(/\s+/g, "-")
    .replace(/[^a-z0-9-]/g, "")
    .replace(/-+/g, "-")
    .replace(/(^-)|(-$)/g, "");
}
