/** Convert string to URL-friendly slug (e.g., "OpenAI Production" → "openai-production") */
export function slugify(str: string): string {
  const slug = str
    .toLowerCase()
    .trim()
    .replace(/\s+/g, "-")
    .replace(/[^a-z0-9-]/g, "")
    .replace(/-+/g, "-")
    .replace(/(^-)|(-$)/g, "");

  // If the result is empty (e.g., input was Chinese characters),
  // generate a fallback ID based on timestamp
  if (!slug && str.trim()) {
    return `group-${Date.now().toString(36)}`;
  }

  return slug;
}

/** Validate ID: only lowercase letters, numbers, and hyphens allowed */
export const isValidId = (id: string): boolean => /^[a-z0-9-]*$/.test(id);
