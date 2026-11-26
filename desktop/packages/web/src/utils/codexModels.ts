import type {
  ConfigOption,
  CustomModel,
  ModeInfo,
  ModelInfo,
  ProviderConfig,
  ProviderModeInfo,
  ProviderModelsResult,
} from '@agentc/protocol';

export interface MergedModelOption {
  modelId: string;
  name: string;
  description: string;
  provider?: string;
  contextWindow?: number;
  supportsReasoning?: boolean;
  supportsTools?: boolean;
  supportsImages?: boolean;
  isCustom?: boolean;
}

export interface MergeModelsOptions {
  mode: 'draft' | 'thread';
  agentId: string;
  sessionAgentId?: string;
  detailModels?: ModelInfo[];
  catalog?: ProviderModelsResult | null;
  localCustomModels?: CustomModel[];
  currentModelId?: string;
}

export interface FilteredModelsResult {
  visible: MergedModelOption[];
  totalMatched: number;
  exactMatch: boolean;
  customCandidateSlug: string | null;
}

export interface ResolvedModeOption {
  id: string;
  name: string;
  desc: string;
  skip: boolean;
}

export interface ResolvedEffortOption {
  id: string;
  label: string;
}

const CODEX_FALLBACK_MODELS: MergedModelOption[] = [
  {
    modelId: 'google/gemini-3-flash-preview',
    name: 'Google: Gemini 3 Flash Preview',
    description: 'Fast frontier reasoning & coding via OpenRouter',
    provider: 'openrouter',
    contextWindow: 1048576,
    supportsReasoning: true,
    supportsTools: true,
    supportsImages: true,
  },
  {
    modelId: 'google/gemini-2.5-flash',
    name: 'Google: Gemini 2.5 Flash',
    description: 'High-throughput coding & tool use via OpenRouter',
    provider: 'openrouter',
    contextWindow: 1048576,
    supportsReasoning: true,
    supportsTools: true,
    supportsImages: true,
  },
  {
    modelId: 'gpt-5.3-codex',
    name: 'GPT-5.3 Codex',
    description: 'OpenAI native Codex model',
    provider: 'openai',
    contextWindow: 400000,
    supportsReasoning: true,
    supportsTools: true,
    supportsImages: true,
  },
];

const CLAUDE_FALLBACK_MODELS: MergedModelOption[] = [
  {
    modelId: 'claude-sonnet-4-5',
    name: 'Claude Sonnet 4.5',
    description: 'Balanced speed and coding intelligence',
    provider: 'anthropic',
  },
  {
    modelId: 'claude-opus-4-1',
    name: 'Claude Opus 4.1',
    description: 'Deep reasoning for hard architecture',
    provider: 'anthropic',
  },
  {
    modelId: 'claude-haiku-4-5',
    name: 'Claude Haiku 4.5',
    description: 'Ultra-fast lightweight edits',
    provider: 'anthropic',
  },
];

/**
 * Returns true when `agentId` supports persisted custom/OpenRouter model slugs (`codex` & custom ACP providers),
 * preventing OpenRouter `vendor/model` slugs from polluting `claude` or `agy` pickers.
 */
export function agentSupportsCustomModels(agentId: string): boolean {
  const id = (agentId ?? '').trim();
  return Boolean(id) && id !== 'claude' && id !== 'agy';
}

/**
 * Merges daemon `providers.models` catalog, saved custom models, and active session
 * detail models without EVER stripping `-latest` or `:variant` (`:thinking`, `:free`, etc.) suffixes,
 * strictly scoping custom models to compatible providers (`agentId`).
 */
export function mergeAvailableModels(opts: MergeModelsOptions): MergedModelOption[] {
  const {
    mode,
    agentId,
    sessionAgentId,
    detailModels,
    catalog,
    localCustomModels,
    currentModelId,
  } = opts;

  const matchingCatalog =
    catalog && (!catalog.provider || catalog.provider === agentId) ? catalog : null;
  const supportsCustom = agentSupportsCustomModels(agentId);

  const out: MergedModelOption[] = [];
  const seen = new Set<string>();

  if (mode === 'draft') {
    const defId = matchingCatalog?.defaultModelId?.trim() || '';
    out.push({
      modelId: '',
      name: defId ? `Provider Default (${defId})` : 'Provider Default',
      description: 'Use provider default model',
      provider: agentId,
    });
    seen.add('');
  }

  const catalogById = new Map(
    (matchingCatalog?.models ?? []).map((m) => [m.modelId, m]),
  );

  // Saved custom models strictly scoped to providers supporting custom slugs
  const customSources: CustomModel[] = supportsCustom
    ? [...(matchingCatalog?.customModels ?? []), ...(localCustomModels ?? [])]
    : [];
  for (const cm of customSources) {
    const slug = cm.slug?.trim();
    if (!slug || seen.has(slug)) continue;
    seen.add(slug);
    const meta = catalogById.get(slug);
    out.push({
      modelId: slug,
      name: cm.name?.trim() || meta?.name || slug,
      description: meta?.description || `Custom model (${slug})`,
      provider: meta?.provider || (slug.includes('/') ? 'openrouter' : agentId),
      contextWindow: meta?.contextWindow,
      supportsReasoning: meta?.supportsReasoning ?? true,
      supportsTools: meta?.supportsTools ?? true,
      supportsImages: meta?.supportsImages,
      isCustom: true,
    });
  }

  let addedProviderModels = 0;

  // Session detail models when inspecting the active thread's agent
  if (mode === 'thread' && (!sessionAgentId || sessionAgentId === agentId) && detailModels?.length) {
    for (const dm of detailModels) {
      const id = dm.modelId;
      if (!id || seen.has(id)) continue;
      seen.add(id);
      const meta = catalogById.get(id);
      out.push({
        modelId: id,
        name: dm.name || meta?.name || id,
        description: dm.description || meta?.description || '',
        provider: meta?.provider || (id.includes('/') ? 'openrouter' : agentId),
        contextWindow: meta?.contextWindow,
        supportsReasoning: meta?.supportsReasoning,
        supportsTools: meta?.supportsTools,
        supportsImages: meta?.supportsImages,
        isCustom: meta?.isCustom,
      });
      addedProviderModels++;
    }
  }

  // Dynamic `providers.models` catalog (works in BOTH Draft and Active Thread!)
  if (matchingCatalog?.models?.length) {
    for (const cm of matchingCatalog.models) {
      if (!cm.modelId || seen.has(cm.modelId)) continue;
      seen.add(cm.modelId);
      out.push({
        modelId: cm.modelId,
        name: cm.name || cm.modelId,
        description: cm.description || '',
        provider: cm.provider,
        contextWindow: cm.contextWindow,
        supportsReasoning: cm.supportsReasoning,
        supportsTools: cm.supportsTools,
        supportsImages: cm.supportsImages,
        isCustom: cm.isCustom,
      });
      addedProviderModels++;
    }
  }

  // Fallback defaults if catalog hasn't loaded or returned 0 models
  if (addedProviderModels === 0) {
    const fallbacks =
      agentId === 'codex'
        ? CODEX_FALLBACK_MODELS
        : agentId === 'claude'
          ? CLAUDE_FALLBACK_MODELS
          : [];
    for (const fb of fallbacks) {
      if (seen.has(fb.modelId)) continue;
      seen.add(fb.modelId);
      out.push({ ...fb });
    }
  }

  // Keep active unlisted custom model visible in picker when already selected on this agent
  const curTrim = currentModelId?.trim() || '';
  if (curTrim && !seen.has(curTrim) && (supportsCustom || !curTrim.includes('/'))) {
    seen.add(curTrim);
    out.splice(mode === 'draft' ? 1 : 0, 0, {
      modelId: curTrim,
      name: curTrim,
      description: 'Active custom model',
      provider: curTrim.includes('/') ? 'openrouter' : agentId,
      isCustom: true,
    });
  }

  return out;
}

export function filterAndRankModels(
  models: MergedModelOption[],
  searchQuery: string,
  favoriteIds: string[] = [],
  maxVisible = 80,
): FilteredModelsResult {
  const rawTrim = searchQuery.trim();
  const q = rawTrim.toLowerCase();
  const favSet = new Set(favoriteIds);

  const matched = !q
    ? models.slice()
    : models.filter(
        (m) =>
          m.modelId.toLowerCase().includes(q) ||
          m.name.toLowerCase().includes(q) ||
          (m.description || '').toLowerCase().includes(q) ||
          (m.provider || '').toLowerCase().includes(q),
      );

  const scoreOf = (m: MergedModelOption): number => {
    if (!q && m.modelId === '') return 10_000;
    let score = 0;
    if (q) {
      const idLow = m.modelId.toLowerCase();
      const nameLow = m.name.toLowerCase();
      if (m.modelId === rawTrim) score += 5_000;
      else if (idLow === q) score += 4_000;
      else if (idLow.startsWith(q)) score += 2_000;
      else if (nameLow.startsWith(q)) score += 1_500;
    }
    if (favSet.has(m.modelId)) score += 500;
    if (m.isCustom) score += 300;
    return score;
  };

  const ranked = matched
    .map((m, idx) => ({ m, idx, score: scoreOf(m) }))
    .sort((a, b) => b.score - a.score || a.idx - b.idx)
    .map((x) => x.m);

  const exactMatch = rawTrim.length > 0 && models.some((m) => m.modelId === rawTrim);
  const customCandidateSlug = rawTrim.length > 0 && !exactMatch ? rawTrim : null;

  return {
    visible: ranked.slice(0, Math.max(1, maxVisible)),
    totalMatched: ranked.length,
    exactMatch,
    customCandidateSlug,
  };
}

export function isFullAccessMode(modeId?: string | null): boolean {
  return (
    modeId === 'full-access' ||
    modeId === 'fullAccess' ||
    modeId === 'bypassPermissions' ||
    modeId === 'danger-full-access'
  );
}

function describeMode(id: string): string {
  switch (id) {
    case 'default':
    case 'supervised':
      return 'Ask before running commands or outside edits';
    case 'plan':
      return 'Read-only exploration & structured plan';
    case 'acceptEdits':
    case 'accept-edits':
    case 'auto-edits':
      return 'Auto-approve file edits, ask for shell';
    case 'auto':
      return 'Workspace write without prompts';
    case 'full-access':
    case 'bypassPermissions':
      return 'Auto-approve all tool calls (skip permissions)';
    default:
      return '';
  }
}

export function resolveAvailableModes(opts: {
  agentId: string;
  detailModes?: ModeInfo[];
  catalogModes?: ProviderModeInfo[];
}): ResolvedModeOption[] {
  const rawList: Array<{ id: string; name: string; description?: string }> =
    opts.detailModes && opts.detailModes.length > 0
      ? opts.detailModes
      : opts.catalogModes && opts.catalogModes.length > 0
        ? opts.catalogModes
        : opts.agentId === 'codex'
          ? [
              { id: 'default', name: 'Build (Supervised)' },
              { id: 'plan', name: 'Plan' },
              { id: 'acceptEdits', name: 'Auto-accept edits' },
              { id: 'auto', name: 'Auto (Workspace write)' },
              { id: 'full-access', name: 'Full access' },
            ]
          : opts.agentId === 'claude'
            ? [
                { id: 'default', name: 'Supervised' },
                { id: 'plan', name: 'Plan' },
                { id: 'acceptEdits', name: 'Auto-accept edits' },
                { id: 'auto', name: 'Auto' },
                { id: 'bypassPermissions', name: 'Full access' },
              ]
            : opts.agentId === 'agy'
              ? [
                  { id: 'default', name: 'Supervised' },
                  { id: 'plan', name: 'Plan' },
                  { id: 'accept-edits', name: 'Auto-accept edits' },
                  { id: 'auto', name: 'Auto' },
                ]
              : [
                  { id: 'supervised', name: 'Supervised' },
                  { id: 'plan', name: 'Plan' },
                  { id: 'acceptEdits', name: 'Auto-accept edits' },
                  { id: 'auto', name: 'Auto' },
                  { id: 'full-access', name: 'Full access' },
                ];

  const seen = new Set<string>();
  const out: ResolvedModeOption[] = [];
  for (const m of rawList) {
    if (!m.id || seen.has(m.id)) continue;
    seen.add(m.id);
    out.push({
      id: m.id,
      name: m.name || m.id,
      desc: m.description || describeMode(m.id),
      skip: isFullAccessMode(m.id),
    });
  }
  return out;
}

/**
 * Resolves the active mode against `availableModes`, handling aliases (`default` <-> `supervised`,
 * `full-access` <-> `bypassPermissions`) and falling back safely to Supervised (`skip: false`)
 * whenever a mode is unsupported by the target provider.
 */
export function coerceSupportedMode(
  desiredMode: string | undefined | null,
  skipPermissions: boolean | undefined,
  availableModes: ResolvedModeOption[],
): ResolvedModeOption {
  const fallbackSupervised: ResolvedModeOption =
    availableModes.find((m) => m.id === 'default' || m.id === 'supervised') ??
    availableModes[0] ?? {
      id: 'default',
      name: 'Supervised',
      desc: describeMode('default'),
      skip: false,
    };

  const raw = (desiredMode ?? '').trim();
  if (!raw) {
    if (skipPermissions) {
      const full = availableModes.find((m) => isFullAccessMode(m.id));
      if (full) return full;
    }
    return fallbackSupervised;
  }

  const exact = availableModes.find((m) => m.id === raw);
  if (exact) return exact;

  // Alias matching across providers
  if (raw === 'default' || raw === 'supervised' || raw === 'build') {
    const sup = availableModes.find((m) => m.id === 'default' || m.id === 'supervised');
    if (sup) return sup;
  }
  if (raw === 'acceptEdits' || raw === 'accept-edits' || raw === 'auto-edits') {
    const ae = availableModes.find(
      (m) => m.id === 'acceptEdits' || m.id === 'accept-edits' || m.id === 'auto-edits',
    );
    if (ae) return ae;
  }
  if (isFullAccessMode(raw)) {
    const full = availableModes.find((m) => isFullAccessMode(m.id));
    if (full) return full;
  }

  return fallbackSupervised;
}

const EFFORT_LABELS: Record<string, string> = {
  '': 'Default',
  low: 'Low',
  medium: 'Medium',
  high: 'High',
  xhigh: 'Extra High',
  max: 'Max',
};

export function formatEffortLabel(id: string): string {
  return EFFORT_LABELS[id] || (id ? id.charAt(0).toUpperCase() + id.slice(1) : 'Default');
}

export function resolveEffortOptions(
  agentId: string,
  detailConfigOptions?: ConfigOption[],
  catalogEfforts?: string[],
): ResolvedEffortOption[] {
  const out: ResolvedEffortOption[] = [{ id: '', label: 'Default' }];
  const seen = new Set<string>(['']);

  const thoughtOpt = detailConfigOptions?.find(
    (o) => o.id === 'thought_level' || o.id === 'thought',
  );

  if (thoughtOpt?.options?.length) {
    for (const opt of thoughtOpt.options) {
      const val = opt.value ?? '';
      if (seen.has(val)) continue;
      seen.add(val);
      out.push({ id: val, label: opt.name || formatEffortLabel(val) });
    }
    return out;
  }

  const rawEfforts =
    catalogEfforts && catalogEfforts.length > 0
      ? catalogEfforts
      : agentId === 'codex'
        ? ['low', 'medium', 'high', 'xhigh']
        : ['low', 'medium', 'high', 'max'];

  for (const lv of rawEfforts) {
    const v = lv.trim();
    if (seen.has(v)) continue;
    seen.add(v);
    out.push({ id: v, label: formatEffortLabel(v) });
  }
  return out;
}

export interface ProviderUpsertPatch {
  id: string;
  name?: string;
  driver?: string;
  enabled?: boolean;
  command?: string;
  args?: string[];
  defaultModel?: string;
  customModels?: CustomModel[];
  icon?: string;
  features?: string[];
  apiKeyInput?: string;
  clearApiKey?: boolean;
}

/**
 * Builds a `ProviderConfig` payload for `providers.upsert` that strictly preserves
 * redacted `"***"` secret env values in `providers.json` when `apiKeyInput` is left blank,
 * updates `OPENROUTER_API_KEY` when a new key is provided, clears it only when
 * `clearApiKey === true`, and preserves custom ACP `icon`, `features`, and `args`.
 */
export function buildProviderUpsertPayload(
  existing: ProviderConfig | undefined | null,
  patch: ProviderUpsertPatch,
): ProviderConfig {
  const id = patch.id;
  const driver =
    patch.driver ??
    existing?.driver ??
    (id === 'codex' || id === 'claude' || id === 'agy' ? id : 'acp');
  const name =
    patch.name ??
    existing?.name ??
    (id === 'codex' ? 'Codex' : id === 'claude' ? 'Claude Code' : id === 'agy' ? 'Antigravity' : id);

  let nextEnv: Record<string, string> | undefined;
  const trimmedKey = (patch.apiKeyInput ?? '').trim();

  if (patch.clearApiKey) {
    nextEnv = { ...(existing?.env ?? {}), OPENROUTER_API_KEY: '' };
  } else if (trimmedKey && trimmedKey !== '***') {
    nextEnv = { ...(existing?.env ?? {}), OPENROUTER_API_KEY: trimmedKey };
  } else if (existing?.env && Object.keys(existing.env).length > 0) {
    nextEnv = { ...existing.env };
  } else {
    nextEnv = undefined;
  }

  const command = patch.command !== undefined ? patch.command.trim() : existing?.command;
  const args = patch.args !== undefined ? patch.args : existing?.args;
  const defaultModel =
    patch.defaultModel !== undefined ? patch.defaultModel.trim() : existing?.defaultModel;
  const customModels =
    patch.customModels !== undefined ? patch.customModels : existing?.customModels;
  const icon = patch.icon !== undefined ? patch.icon.trim() : existing?.icon;
  const features = patch.features !== undefined ? patch.features : existing?.features;

  return {
    id,
    name,
    driver,
    enabled: patch.enabled ?? existing?.enabled ?? true,
    builtin: existing?.builtin ?? (id === 'codex' || id === 'claude' || id === 'agy'),
    ...(command ? { command } : {}),
    ...(args && args.length > 0 ? { args } : {}),
    ...(nextEnv !== undefined ? { env: nextEnv } : {}),
    ...(defaultModel ? { defaultModel } : {}),
    ...(customModels !== undefined ? { customModels } : {}),
    ...(icon ? { icon } : {}),
    ...(features && features.length > 0 ? { features } : {}),
  };
}

export function formatContextWindow(tokens?: number): string {
  if (!tokens || tokens <= 0) return '';
  if (tokens >= 1_000_000) {
    const m = tokens / 1_000_000;
    return Number.isInteger(m) ? `${m}M` : `${m.toFixed(1)}M`;
  }
  if (tokens >= 1_000) {
    return `${Math.round(tokens / 1_000)}k`;
  }
  return String(tokens);
}
