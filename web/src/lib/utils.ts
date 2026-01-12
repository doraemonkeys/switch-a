/** Generate a random ID string (6 characters, alphanumeric) */
export function generateRandomId(length: number = 6): string {
  const chars = "abcdefghijklmnopqrstuvwxyz0123456789";
  const randomValues = new Uint32Array(length);
  crypto.getRandomValues(randomValues);
  return Array.from(randomValues, (v) => chars[v % chars.length]).join("");
}

/** Convert string to URL-friendly slug with random suffix (e.g., "OpenAI Production" → "openai-production-abc123") */
export function slugify(str: string): string {
  const randomSuffix = generateRandomId(6);

  const slug = str
    .toLowerCase()
    .trim()
    .replace(/\s+/g, "-")
    .replace(/[^a-z0-9-]/g, "")
    .replace(/-+/g, "-")
    .replace(/(^-)|(-$)/g, "");

  // If the result is empty (e.g., input was Chinese characters),
  // generate a fallback ID based on random suffix only
  if (!slug && str.trim()) {
    return `item-${randomSuffix}`;
  }

  // Append random suffix to make ID unique
  return slug ? `${slug}-${randomSuffix}` : randomSuffix;
}

/** Validate ID: only lowercase letters, numbers, and hyphens allowed */
export const isValidId = (id: string): boolean => /^[a-z0-9-]*$/.test(id);
