// Persist only numeric usage fields. Upstream error bodies and arbitrary metadata stay out of records.
export function usageOf(data) {
  const u = data?.usage;
  if (!u || typeof u !== 'object') return null;
  const safe = value => Number.isFinite(value) && value >= 0 ? Math.floor(value) : null;
  const result = {
    input_tokens: safe(u.input_tokens ?? u.prompt_tokens),
    output_tokens: safe(u.output_tokens ?? u.completion_tokens),
    cached_tokens: safe(u.input_tokens_details?.cached_tokens ?? u.prompt_tokens_details?.cached_tokens),
  };
  const reasoning = safe(u.output_tokens_details?.reasoning_tokens ?? u.completion_tokens_details?.reasoning_tokens ?? u.reasoning_tokens);
  if (reasoning !== null) result.reasoning_tokens = reasoning;
  return result;
}
