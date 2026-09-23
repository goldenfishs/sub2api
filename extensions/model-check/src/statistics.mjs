const completedStatuses = new Set(['normal', 'review', 'failed']);

export const HISTORY_LIMIT = 30;

export function summarizeRuns(runs, limit = HISTORY_LIMIT) {
  const completed = runs.filter(run => completedStatuses.has(run.status)).sort((a, b) => b.created_at - a.created_at).slice(0, limit);
  const counts = { normal: 0, review: 0, failed: 0 };
  for (const run of completed) counts[run.status]++;
  const judged = counts.normal + counts.review;
  const latest = completed[0];
  return {
    limit, total: completed.length, judged, ...counts,
    pass_rate: judged ? counts.normal / judged : null,
    success_rate: completed.length ? judged / completed.length : null,
    latest_duration_ms: Number.isFinite(latest?.total_ms) ? latest.total_ms : null,
    history: completed.slice().reverse().map(run => ({
      id: run.id, status: run.status, created_at: run.created_at,
      duration_ms: Number.isFinite(run.total_ms) ? run.total_ms : null,
    })),
  };
}
