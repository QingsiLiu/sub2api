// geili: GPT group IQ monitor page.
export default {
  gptIq: {
    nav: 'GPT IQ',
    title: 'Group IQ Monitor',
    description: 'A fresh session runs the same reasoning question every 2 minutes, so you can see whether any GPT group has been dumbed down',
    eyebrow: 'GPT groups',
    subtitle: 'Last 60 minutes · fresh session every {interval} min · answer {answer} means normal',
    updatedAt: 'Updated {time}',
    waiting: 'Waiting for the first round',
    nextRefresh: 'Auto refresh in {s}s',
    refreshing: 'Refreshing',
    refresh: 'Refresh',
    config: {
      model: 'Model',
      reasoning: 'Reasoning',
      interval: 'Interval',
      intervalValue: '{n} min',
      answer: 'Expected'
    },
    status: {
      normal: 'Normal',
      degraded: 'Degraded',
      pending: 'Checking',
      error: 'Timeout / error',
      skipped: 'No result'
    },
    legend: {
      skipped: 'None / empty'
    },
    hint: 'A red cell now and then is expected: every check is a brand-new session, so one wrong answer does not mean the group is broken. Only several red cells in a row, or your own sessions feeling noticeably worse, mean the group is unstable right now. Blue is a timeout or gateway hiccup, not an IQ result.',
    group: {
      normalRate: 'Accuracy, last 60 min',
      noJudged: 'Not judged yet',
      streak: 'Last {n} in a row: {status}',
      counts: 'Normal {normal} · Degraded {degraded} · Error {error} · None {skipped}',
      axisPast: '60 min ago',
      axisNow: 'Now',
      cellLabel: '{time} {status}',
      lightsLabel: '{name}, results of the last 60 minutes'
    },
    errors: {
      authFailed: 'The monitor service could not confirm your session. Please refresh in a moment; if it keeps happening, sign out and sign in again.',
      unavailable: 'Could not load group status. Please try again later.',
      stale: 'Refresh failed; showing the previous result.',
      adminOnly: 'Only admins can delete drawings.',
      deleteFailed: 'Could not delete the drawing. Please try again later.'
    },
    idle: 'No probe results in the last 60 minutes. If probing was just enabled, refresh after this round finishes.',
    retry: 'Retry',
    question: {
      kicker: 'Reasoning question',
      title: 'Lactase genetics height question',
      lede: 'Asked in a fresh session every {interval} min; only the number in the final reply counts.',
      expand: 'Show full question',
      collapse: 'Collapse',
      answerLabel: 'Expected answer',
      answerUnit: 'inches',
      rule: 'Only the final reply body is scored: the last standalone number on the last line, otherwise the last number in the body. {answer} means normal. Mentioning {answer} anywhere else, or in the reasoning, does not count.'
    },
    drawings: {
      kicker: 'Drawing test',
      lede: 'Checked every {interval} min · last {keep} successful drawings kept per group',
      empty: 'Nothing drawn for this group yet. After drawing probes are enabled, refresh when this round finishes.',
      noSvg: 'No usable SVG this time',
      alt: 'SpongeBob riding a bike, drawn by {group} at {time}',
      enlarge: 'View larger',
      delete: 'Delete',
      deleting: 'Deleting',
      confirmTitle: 'Delete this drawing?',
      confirmMessage: 'Once deleted, no user will see it.',
      ok: 'Drawn',
      error: 'Failed',
      skipped: 'Skipped',
      pendingAccount: 'Unattributed',
      elapsed: 'Took {time}'
    },
    admin: {
      kicker: 'Admins only',
      title: 'Last 20 checks',
      lede: 'Regular users cannot see upstream accounts.',
      reasoningTab: 'Reasoning',
      drawingTab: 'Drawing',
      emptyReasoning: 'No reasoning records yet. Account names appear after the next round.',
      emptyDrawing: 'No drawing records yet. Account names appear after the next round.',
      time: 'Time',
      group: 'Group',
      result: 'Result',
      answer: 'Answer',
      elapsed: 'Elapsed',
      reason: 'Reason',
      account: 'Account',
      attempts: '{n} attempts',
      stream: 'stream',
      nonStream: 'non-stream',
      budget: 'Out of time budget, no more retries; the last real result is kept above.'
    },
    detail: {
      extracted: 'Extracted final number',
      expected: 'Expected',
      noAnswer: 'No number extracted',
      account: 'Upstream account',
      reply: 'Final reply',
      close: 'Close'
    },
    reasons: {
      no_sample: 'No record for this slot (the previous round had not finished, so the timer did not fire)',
      probe_busy: 'The previous round was still running; this slot was skipped',
      missing_key: 'No probe key configured; no request sent',
      ok: 'Extracted {answer}',
      wrong_answer: 'The extracted number is not {answer}',
      empty_response: 'Got a reply but no number could be extracted',
      incomplete: 'Upstream reply was incomplete',
      no_svg: 'The reply contained no SVG',
      svg_rejected: 'The SVG could not be displayed safely',
      draw_busy: 'The previous drawing round was still running; not added to the gallery',
      probe_timeout: 'Probe timed out',
      timeout: 'Probe timed out',
      non_stream_not_allowed: 'This account only accepts streaming; the probe switches to streaming',
      empty_stream: 'The streamed reply was empty',
      upstream_transport: 'Upstream connection error',
      output_limit: 'Output hit the length limit',
      response_failed: 'Upstream generation failed',
      http: 'Upstream returned HTTP {code}'
    },
    reply: {
      no_sample: '(No record for this slot. While the previous round is still running, the timer does not fire and the slot stays empty.)',
      probe_busy: '(The previous round was still running; this slot was skipped.)',
      missing_key: '(This group has no probe key yet; no request sent.)',
      none: '(No reply body was saved for this check)'
    },
    duration: {
      underSecond: 'under 1s',
      seconds: '{s}s',
      minutes: '{m}m {s}s'
    }
  }
}
