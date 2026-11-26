import { describe, expect, it } from 'vitest';
import type { ProviderConfig, ProviderModelsResult } from '@agentc/protocol';
import {
  agentSupportsCustomModels,
  buildProviderUpsertPayload,
  coerceSupportedMode,
  filterAndRankModels,
  formatContextWindow,
  isFullAccessMode,
  mergeAvailableModels,
  resolveAvailableModes,
  resolveEffortOptions,
} from './codexModels.js';

describe('codexModels utilities', () => {
  const sampleCatalog: ProviderModelsResult = {
    provider: 'codex',
    defaultModelId: 'google/gemini-3-flash-preview',
    openRouterConfigured: true,
    nativeAuthConfigured: false,
    models: [
      {
        modelId: 'google/gemini-3-flash-preview',
        name: 'Google: Gemini 3 Flash Preview',
        description: 'Fast frontier model',
        provider: 'openrouter',
        contextWindow: 1048576,
        supportsReasoning: true,
        supportsTools: true,
        supportsImages: true,
        isCustom: false,
      },
      {
        modelId: 'openai/gpt-5.4-mini-latest:thinking',
        name: 'OpenAI GPT-5.4 Mini Latest (Thinking)',
        description: 'Variant with thinking suffix',
        provider: 'openrouter',
        contextWindow: 200000,
        supportsReasoning: true,
        supportsTools: true,
        supportsImages: true,
        isCustom: false,
      },
    ],
    customModels: [
      { slug: 'deepseek/deepseek-v4.1-flash:free', name: 'DeepSeek V4.1 Free' },
    ],
    availableModes: [
      { id: 'default', name: 'Build (Supervised)' },
      { id: 'plan', name: 'Plan' },
      { id: 'acceptEdits', name: 'Auto-accept edits' },
      { id: 'auto', name: 'Auto (Workspace write)' },
      { id: 'full-access', name: 'Full access' },
    ],
    reasoningEfforts: ['low', 'medium', 'high', 'xhigh'],
  };

  it('merges catalog + custom + local custom models and NEVER strips -latest or :variant suffixes', () => {
    const mergedDraft = mergeAvailableModels({
      mode: 'draft',
      agentId: 'codex',
      catalog: sampleCatalog,
      localCustomModels: [
        { slug: 'openrouter/openai/gpt-5.4-nano-latest:thinking', name: 'openrouter/openai/gpt-5.4-nano-latest:thinking' },
        { slug: 'deepseek/deepseek-v4.1-flash:free', name: 'Duplicate should dedup' },
      ],
    });

    expect(mergedDraft[0].modelId).toBe('');
    expect(mergedDraft[0].name).toContain('Provider Default');
    const ids = mergedDraft.map((m) => m.modelId);
    expect(ids).toContain('deepseek/deepseek-v4.1-flash:free');
    expect(ids).toContain('openrouter/openai/gpt-5.4-nano-latest:thinking');
    expect(ids).toContain('openai/gpt-5.4-mini-latest:thinking');
    expect(ids.filter((id) => id === 'deepseek/deepseek-v4.1-flash:free')).toHaveLength(1);
  });

  it('strictly prevents Codex/OpenRouter custom slugs from polluting claude or agy model lists (CHAL-1)', () => {
    expect(agentSupportsCustomModels('codex')).toBe(true);
    expect(agentSupportsCustomModels('custom-acp')).toBe(true);
    expect(agentSupportsCustomModels('claude')).toBe(false);
    expect(agentSupportsCustomModels('agy')).toBe(false);

    const claudeThread = mergeAvailableModels({
      mode: 'thread',
      agentId: 'claude',
      catalog: sampleCatalog, // even if a mismatched codex catalog were passed
      localCustomModels: [
        { slug: 'deepseek/deepseek-r1:free', name: 'DeepSeek R1 Free' },
        { slug: 'openrouter/openai/gpt-5.4-nano-latest:thinking', name: 'GPT Nano' },
      ],
    });
    const rankedClaude = filterAndRankModels(claudeThread, '', [], 80);
    expect(rankedClaude.visible.map((m) => m.modelId)).not.toContain('deepseek/deepseek-r1:free');
    expect(rankedClaude.visible.map((m) => m.modelId)).not.toContain('openrouter/openai/gpt-5.4-nano-latest:thinking');
    expect(rankedClaude.visible[0].modelId).toBe('claude-sonnet-4-5');

    const agyDraft = mergeAvailableModels({
      mode: 'draft',
      agentId: 'agy',
      localCustomModels: [{ slug: 'deepseek/deepseek-r1:free', name: 'DeepSeek R1 Free' }],
    });
    expect(agyDraft.map((m) => m.modelId)).toEqual(['']);
  });

  it('caps visible results at 80 and surfaces customCandidateSlug when search has no exact match', () => {
    const bigCatalog: ProviderModelsResult = {
      ...sampleCatalog,
      models: Array.from({ length: 220 }, (_, i) => ({
        modelId: `vendor/model-${i}`,
        name: `Vendor Model ${i}`,
        description: 'OpenRouter model',
        provider: 'openrouter',
        contextWindow: 128000,
        supportsReasoning: true,
        supportsTools: true,
        supportsImages: false,
        isCustom: false,
      })),
      customModels: [],
    };
    const merged = mergeAvailableModels({ mode: 'draft', agentId: 'codex', catalog: bigCatalog });

    const unfiltered = filterAndRankModels(merged, '', ['vendor/model-199'], 80);
    expect(unfiltered.visible).toHaveLength(80);
    expect(unfiltered.totalMatched).toBe(221); // 220 + Provider Default
    expect(unfiltered.customCandidateSlug).toBeNull();
    // Provider Default first when query empty, followed by favorite
    expect(unfiltered.visible[0].modelId).toBe('');
    expect(unfiltered.visible[1].modelId).toBe('vendor/model-199');

    // Arbitrary unlisted OpenRouter slug with -latest:thinking suffix
    const unlisted = filterAndRankModels(
      merged,
      '  openrouter/openai/gpt-5.4-nano-latest:thinking  ',
      [],
      80,
    );
    expect(unlisted.exactMatch).toBe(false);
    expect(unlisted.customCandidateSlug).toBe('openrouter/openai/gpt-5.4-nano-latest:thinking');

    // Exact match suppresses duplicate customCandidateSlug
    const exact = filterAndRankModels(merged, 'vendor/model-42', [], 80);
    expect(exact.exactMatch).toBe(true);
    expect(exact.customCandidateSlug).toBeNull();
    expect(exact.visible[0].modelId).toBe('vendor/model-42');
  });

  it('resolves dynamic modes and coerces unsupported modes safely back to Supervised (skip: false)', () => {
    const codexModes = resolveAvailableModes({
      agentId: 'codex',
      catalogModes: sampleCatalog.availableModes,
    });
    expect(codexModes.map((m) => m.id)).toEqual([
      'default',
      'plan',
      'acceptEdits',
      'auto',
      'full-access',
    ]);
    expect(isFullAccessMode('full-access')).toBe(true);
    expect(isFullAccessMode('bypassPermissions')).toBe(true);
    expect(isFullAccessMode('auto')).toBe(false);

    // Alias 'supervised' maps to Codex 'default' (Build (Supervised))
    expect(coerceSupportedMode('supervised', false, codexModes).id).toBe('default');
    // Explicit session mode 'plan' takes precedence even if legacy skipPermissions was true
    expect(coerceSupportedMode('plan', true, codexModes).id).toBe('plan');
    // Unsupported unknown mode falls back safely to Supervised (never full-access!)
    const restrictedModes = [
      { id: 'default', name: 'Build (Supervised)', desc: '', skip: false },
      { id: 'plan', name: 'Plan', desc: '', skip: false },
    ];
    const fallback = coerceSupportedMode('non-existent-mode', true, restrictedModes);
    expect(fallback.id).toBe('default');
    expect(fallback.skip).toBe(false);
  });

  it('resolves effort options including Default ("") and Codex xhigh', () => {
    const codexEfforts = resolveEffortOptions('codex', undefined, sampleCatalog.reasoningEfforts);
    expect(codexEfforts.map((e) => e.id)).toEqual(['', 'low', 'medium', 'high', 'xhigh']);
    expect(codexEfforts.find((e) => e.id === 'xhigh')?.label).toBe('Extra High');

    const claudeEfforts = resolveEffortOptions('claude');
    expect(claudeEfforts.map((e) => e.id)).toEqual(['', 'low', 'medium', 'high', 'max']);
  });

  it('buildProviderUpsertPayload preserves "***" secrets, updates/clears OPENROUTER_API_KEY, and preserves ACP icon/features/args (CHAL-3)', () => {
    const existingRedacted: ProviderConfig = {
      id: 'codex',
      name: 'Codex',
      driver: 'codex',
      enabled: true,
      builtin: true,
      command: '~/.local/bin/codex',
      env: {
        OPENROUTER_API_KEY: '***',
        OPENAI_API_KEY: '***',
      },
      defaultModel: 'google/gemini-2.5-flash',
      customModels: [{ slug: 'a/b', name: 'A B' }],
    };

    // 1. Blank apiKeyInput -> MUST preserve "***" for both keys!
    const savedNonSecret = buildProviderUpsertPayload(existingRedacted, {
      id: 'codex',
      defaultModel: 'google/gemini-3-flash-preview',
      apiKeyInput: '   ',
    });
    expect(savedNonSecret.env).toEqual({
      OPENROUTER_API_KEY: '***',
      OPENAI_API_KEY: '***',
    });
    expect(savedNonSecret.defaultModel).toBe('google/gemini-3-flash-preview');
    expect(savedNonSecret.command).toBe('~/.local/bin/codex');

    // 2. New apiKeyInput -> updates OPENROUTER_API_KEY while preserving OPENAI_API_KEY: "***"
    const updatedKey = buildProviderUpsertPayload(existingRedacted, {
      id: 'codex',
      apiKeyInput: 'sk-or-test-new-secret',
    });
    expect(updatedKey.env).toEqual({
      OPENROUTER_API_KEY: 'sk-or-test-new-secret',
      OPENAI_API_KEY: '***',
    });

    // 3. Explicit clearApiKey -> sets OPENROUTER_API_KEY: '' (which daemon Upsert deletes) while preserving OPENAI_API_KEY: "***"
    const clearedKey = buildProviderUpsertPayload(existingRedacted, {
      id: 'codex',
      clearApiKey: true,
    });
    expect(clearedKey.env).toEqual({
      OPENROUTER_API_KEY: '',
      OPENAI_API_KEY: '***',
    });

    // 4. Custom ACP provider -> MUST preserve icon, features, and args when updating customModels (CHAL-3)
    const customAcp: ProviderConfig = {
      id: 'custom-acp',
      name: 'Custom ACP',
      driver: 'acp',
      enabled: true,
      builtin: false,
      command: '/usr/local/bin/my-acp',
      args: ['--stdio', '--fast'],
      icon: 'sparkles',
      features: ['thinking', 'customModels'],
    };
    const updatedAcp = buildProviderUpsertPayload(customAcp, {
      id: 'custom-acp',
      customModels: [{ slug: 'vendor/custom-1', name: 'Custom 1' }],
    });
    expect(updatedAcp.icon).toBe('sparkles');
    expect(updatedAcp.features).toEqual(['thinking', 'customModels']);
    expect(updatedAcp.args).toEqual(['--stdio', '--fast']);
    expect(updatedAcp.builtin).toBe(false);
    expect(updatedAcp.customModels).toEqual([{ slug: 'vendor/custom-1', name: 'Custom 1' }]);

    expect(formatContextWindow(1048576)).toBe('1.0M');
    expect(formatContextWindow(128000)).toBe('128k');
  });
});

describe('D-07 agy mode resolution and accept-edits alias coercion', () => {
  it('resolveAvailableModes returns exact agy modes [default, plan, accept-edits, auto] without supervised or acceptEdits', () => {
    const agyModes = resolveAvailableModes({ agentId: 'agy' });
    expect(agyModes.map((m) => m.id)).toEqual(['default', 'plan', 'accept-edits', 'auto']);
    const ae = agyModes.find((m) => m.id === 'accept-edits');
    expect(ae?.desc).toBe('Auto-approve file edits, ask for shell');
  });

  it('coerceSupportedMode maps acceptEdits <-> accept-edits <-> auto-edits equivalently across providers', () => {
    const agyModes = resolveAvailableModes({ agentId: 'agy' });
    const codexModes = resolveAvailableModes({ agentId: 'codex' });
    const customModes = resolveAvailableModes({
      agentId: 'custom',
      detailModes: [
        { id: 'default', name: 'Default' },
        { id: 'auto-edits', name: 'Auto edits' },
      ],
    });

    expect(coerceSupportedMode('acceptEdits', false, agyModes).id).toBe('accept-edits');
    expect(coerceSupportedMode('auto-edits', false, agyModes).id).toBe('accept-edits');
    expect(coerceSupportedMode('accept-edits', false, codexModes).id).toBe('acceptEdits');
    expect(coerceSupportedMode('auto-edits', false, codexModes).id).toBe('acceptEdits');
    expect(coerceSupportedMode('acceptEdits', false, customModes).id).toBe('auto-edits');
    expect(coerceSupportedMode('accept-edits', false, customModes).id).toBe('auto-edits');
  });
});
