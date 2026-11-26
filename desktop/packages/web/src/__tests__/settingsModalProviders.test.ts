import { describe, expect, it } from 'vitest';
import {
  beginHydrateProvidersSeq,
  beginRefreshConfigsOnlySeq,
  isCurrentHydrateProvidersSeq,
  isCurrentRefreshConfigsOnlySeq,
  resetProvidersRequestTracker,
  type ProvidersRequestTracker,
} from '../components/views/SettingsModal.js';

describe('SettingsModal concurrent hydrateProvidersForHost & refreshProviderConfigsOnly (D-10)', () => {
  it('never leaves providersFetching stuck true when refreshProviderConfigsOnly overlaps in-flight hydrateProvidersForHost', async () => {
    const tracker: ProvidersRequestTracker = { hydrateSeq: 0, configsSeq: 0 };
    let providersFetching = false;

    let resolveHydrate!: () => void;
    const hydratePromise = (async () => {
      const seq = beginHydrateProvidersSeq(tracker);
      providersFetching = true;
      try {
        await new Promise<void>((r) => {
          resolveHydrate = r;
        });
      } finally {
        if (isCurrentHydrateProvidersSeq(tracker, seq)) {
          providersFetching = false;
        }
      }
    })();

    // Start overlapping refreshProviderConfigsOnly while hydrateProvidersForHost is still awaiting RPC!
    const refreshToken = beginRefreshConfigsOnlySeq(tracker);
    expect(isCurrentRefreshConfigsOnlySeq(tracker, refreshToken)).toBe(true);

    // Resolve hydrateProvidersForHost -> finally MUST still clear providersFetching to false!
    resolveHydrate();
    await hydratePromise;
    expect(providersFetching).toBe(false);
  });

  it('invalidates stale refreshProviderConfigsOnly when a new hydrateProvidersForHost or host/modal reset occurs', () => {
    const tracker: ProvidersRequestTracker = { hydrateSeq: 0, configsSeq: 0 };
    const staleRefresh = beginRefreshConfigsOnlySeq(tracker);
    const hydrateSeq = beginHydrateProvidersSeq(tracker);

    expect(isCurrentRefreshConfigsOnlySeq(tracker, staleRefresh)).toBe(false);
    expect(isCurrentHydrateProvidersSeq(tracker, hydrateSeq)).toBe(true);

    resetProvidersRequestTracker(tracker);
    expect(isCurrentHydrateProvidersSeq(tracker, hydrateSeq)).toBe(false);
  });
});
