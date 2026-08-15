const generatedAt = '2026-08-14T08:00:00Z'
const usdCnyReference = 6.8

export const mockUser = {
  id: 1001,
  username: 'pricing-reviewer',
  email: 'reviewer@example.invalid',
  avatar_url: null,
  role: 'admin',
  balance: 128.64,
  frozen_balance: 0,
  concurrency: 10,
  rpm_limit: 0,
  status: 'active',
  allowed_groups: [4, 27, 41, 46, 67, 82],
  balance_notify_enabled: false,
  balance_notify_threshold: null,
  balance_notify_extra_emails: [],
  created_at: '2026-01-01T00:00:00Z',
  updated_at: generatedAt,
  run_mode: 'standard',
}

export const publicSettings = {
  site_name: 'Sub2API',
  site_subtitle: 'AI API Gateway',
  site_logo: '',
  api_base_url: 'https://api.example.invalid',
  doc_url: '',
  contact_info: '',
  registration_enabled: true,
  email_verify_enabled: false,
  force_email_on_third_party_signup: false,
  registration_email_suffix_whitelist: [],
  promo_code_enabled: false,
  password_reset_enabled: true,
  invitation_code_enabled: false,
  payment_enabled: false,
  risk_control_enabled: false,
  available_channels_enabled: true,
  service_quota_enabled: false,
  affiliate_enabled: false,
  backend_mode_enabled: false,
  custom_menu_items: [],
  server_utc_offset: '-07:00',
}

const groups = [
  { id: 4, name: 'Plus/Pro 混合号池', platform: 'openai', rate: '0.1', currency: 'usd_quota' },
  { id: 27, name: 'Pro 更稳定号池', platform: 'openai', rate: '0.2', currency: 'usd_quota' },
  { id: 80, name: 'GPT 破甲', platform: 'openai', rate: '0.1', currency: 'usd_quota' },
  { id: 47, name: 'CC-Kiro', platform: 'anthropic', rate: '0.1', currency: 'usd_quota' },
  { id: 46, name: 'CC-满血 Max', platform: 'anthropic', rate: '0.2', currency: 'usd_quota' },
  { id: 41, name: 'Gemini', platform: 'gemini', rate: '0.1', currency: 'usd_quota' },
  { id: 67, name: 'Grok', platform: 'grok', rate: '0.2', currency: 'usd_quota' },
  { id: 82, name: '国模综合', platform: 'openai', rate: '0.2', currency: 'native_cny' },
]

function groupView(group, personalized = false) {
  const personalRates = { 27: '0.16', 46: '0.18' }
  const personal = personalized && personalRates[group.id]
  return {
    id: group.id,
    name: group.name,
    platform: group.platform,
    default_multiplier: group.rate,
    effective_multiplier: personal || group.rate,
    multiplier_source: personal ? 'user' : 'default',
    currency_mode: group.currency,
    peak_rate_enabled: group.id === 27,
    peak_rate_active: false,
    peak_start: group.id === 27 ? '18:00' : '',
    peak_end: group.id === 27 ? '23:00' : '',
    peak_rate_multiplier: group.id === 27 ? '1.15' : '1',
  }
}

function formatFixturePrice(value, places = 8) {
  return Number(value.toFixed(places)).toString()
}

function tokenItem(key, official, _officialCny, base, multiplier, actual, _savings, _percent, options = {}) {
  const officialCurrency = options.officialCurrency || 'USD'
  const officialCny = official === null
    ? null
    : Number(official) * (officialCurrency === 'CNY' ? 1 : usdCnyReference)
  const savings = officialCny === null ? null : officialCny - Number(actual)
  const percent = officialCny
    ? (savings / officialCny) * 100
    : null
  return {
    key,
    unit: '1M_tokens',
    official_price: official,
    official_currency: officialCurrency,
    official_cny_price: officialCny === null ? null : formatFixturePrice(officialCny),
    system_base_price: base,
    system_base_currency: options.baseCurrency || 'USD',
    effective_multiplier: multiplier,
    multiplier_source: options.multiplierSource || 'default',
    actual_cny_price: actual,
    savings_cny: savings === null ? null : formatFixturePrice(savings),
    savings_percent: percent === null ? null : formatFixturePrice(percent, 2),
    comparison_status: official === null ? 'unavailable' : 'exact',
    ...(options.tierLabel ? { tier_label: options.tierLabel } : {}),
    ...(options.minContextTokens ? { min_context_tokens: options.minContextTokens } : {}),
  }
}

function model(modelId, displayName, provider, billingMode, source, from, groupPrices) {
  return {
    model_id: modelId,
    display_name: displayName,
    provider,
    billing_mode: billingMode,
    comparison_status: source ? 'exact' : 'unavailable',
    ...(source ? {
      official_reference_model: modelId === 'gpt-5.6' ? 'gpt-5.6-sol' : modelId,
      official_source: source,
      official_effective_from: from,
    } : {}),
    group_prices: groupPrices,
  }
}

function buildModels(personalized = false) {
  const rate27 = personalized ? '0.16' : '0.2'
  const rate46 = personalized ? '0.18' : '0.2'
  const sourceOpenAI = 'https://developers.openai.com/api/docs/pricing'
  const sourceAnthropic = 'https://docs.anthropic.com/en/docs/about-claude/pricing'
  const sourceGemini = 'https://ai.google.dev/gemini-api/docs/pricing'
  const sourceGrok = 'https://docs.x.ai/developers/models'
  const sourceDeepSeek = 'https://api-docs.deepseek.com/zh-cn/quick_start/pricing/'

  return [
    model('gpt-5.6', 'GPT-5.6 (Sol)', 'openai', 'token', sourceOpenAI, '2026-07-09', [
      {
        group_id: 4,
        billing_mode: 'token',
        price_source: 'channel',
        items: [
          tokenItem('input', '5', '35.9', '5', '0.1', '0.5', '35.4', '98.61'),
          tokenItem('output', '30', '215.4', '30', '0.1', '3', '212.4', '98.61'),
          tokenItem('cache_read', '0.5', '3.59', '0.5', '0.1', '0.05', '3.54', '98.61'),
          tokenItem('cache_write', '6.25', '44.875', '6.25', '0.1', '0.625', '44.25', '98.61'),
          tokenItem('input:long_context', '10', '71.8', '10', '0.1', '1', '70.8', '98.61', { minContextTokens: 272001 }),
          tokenItem('output:long_context', '45', '323.1', '45', '0.1', '4.5', '318.6', '98.61', { minContextTokens: 272001 }),
        ],
      },
    ]),
    model('gpt-5.6-terra', 'GPT-5.6 Terra', 'openai', 'token', sourceOpenAI, '2026-07-30', [
      {
        group_id: 4,
        billing_mode: 'token',
        price_source: 'channel',
        items: [
          tokenItem('input', '2', '13.6', '2.5', '0.1', '0.25', '13.35', '98.16'),
          tokenItem('output', '12', '81.6', '15', '0.1', '1.5', '80.1', '98.16'),
          tokenItem('cache_read', '0.2', '1.36', '0.25', '0.1', '0.025', '1.335', '98.16'),
          tokenItem('cache_write', '2.5', '17', '3.125', '0.1', '0.3125', '16.6875', '98.16'),
        ],
      },
      {
        group_id: 27,
        billing_mode: 'token',
        price_source: 'channel',
        items: [
          tokenItem('input', '2', '14.36', '2.5', rate27, personalized ? '0.4' : '0.5', personalized ? '13.96' : '13.86', personalized ? '97.21' : '96.52', { multiplierSource: personalized ? 'user' : 'default' }),
          tokenItem('output', '12', '86.16', '15', rate27, personalized ? '2.4' : '3', personalized ? '83.76' : '83.16', personalized ? '97.21' : '96.52', { multiplierSource: personalized ? 'user' : 'default' }),
          tokenItem('cache_read', '0.2', '1.436', '0.25', rate27, personalized ? '0.04' : '0.05', personalized ? '1.396' : '1.386', personalized ? '97.21' : '96.52', { multiplierSource: personalized ? 'user' : 'default' }),
          tokenItem('cache_write', '2.5', '17.95', '3.125', rate27, personalized ? '0.5' : '0.625', personalized ? '17.45' : '17.325', personalized ? '97.21' : '96.52', { multiplierSource: personalized ? 'user' : 'default' }),
        ],
      },
    ]),
    model('gpt-5.5', 'GPT-5.5', 'openai', 'token', sourceOpenAI, '2026-04-24', [
      {
        group_id: 4,
        billing_mode: 'token',
        price_source: 'fallback',
        items: [
          tokenItem('input', '5', '34', '2.5', '0.1', '0.25', '33.75', '99.26'),
          tokenItem('output', '30', '204', '15', '0.1', '1.5', '202.5', '99.26'),
          tokenItem('cache_read', '0.5', '3.4', '0.25', '0.1', '0.025', '3.375', '99.26'),
        ],
      },
      {
        group_id: 80,
        billing_mode: 'token',
        price_source: 'fallback',
        items: [
          tokenItem('input', '5', '35.9', '2.5', '0.1', '0.25', '35.65', '99.3'),
          tokenItem('output', '30', '204', '15', '0.1', '1.5', '202.5', '99.26'),
          tokenItem('cache_read', '0.5', '3.59', '0.25', '0.1', '0.025', '3.565', '99.3'),
        ],
      },
    ]),
    model('gpt-5.4', 'GPT-5.4', 'openai', 'token', sourceOpenAI, '2026-03-05', [
      {
        group_id: 4,
        billing_mode: 'token',
        price_source: 'fallback',
        items: [
          tokenItem('input', '2.5', '17', '2.5', '0.1', '0.25', '16.75', '98.53'),
          tokenItem('output', '15', '102', '15', '0.1', '1.5', '100.5', '98.53'),
          tokenItem('cache_read', '0.25', '1.7', '0.25', '0.1', '0.025', '1.675', '98.53'),
          tokenItem('input:long_context', '5', '34', '5', '0.1', '0.5', '33.5', '98.53', { minContextTokens: 272001 }),
          tokenItem('output:long_context', '22.5', '153', '22.5', '0.1', '2.25', '150.75', '98.53', { minContextTokens: 272001 }),
        ],
      },
    ]),
    model('claude-opus-5', 'Claude Opus 5', 'anthropic', 'token', sourceAnthropic, '2026-07-24', [
      {
        group_id: 46,
        billing_mode: 'token',
        price_source: 'channel',
        items: [
          tokenItem('input', '5', '35.9', '5', rate46, personalized ? '0.9' : '1', personalized ? '35' : '34.9', personalized ? '97.49' : '97.21', { multiplierSource: personalized ? 'user' : 'default' }),
          tokenItem('output', '25', '179.5', '25', rate46, personalized ? '4.5' : '5', personalized ? '175' : '174.5', personalized ? '97.49' : '97.21', { multiplierSource: personalized ? 'user' : 'default' }),
          tokenItem('cache_write_5m', '6.25', '44.875', '6.25', rate46, personalized ? '1.125' : '1.25', personalized ? '43.75' : '43.625', personalized ? '97.49' : '97.21', { multiplierSource: personalized ? 'user' : 'default' }),
          tokenItem('cache_write_1h', '10', '71.8', '10', rate46, personalized ? '1.8' : '2', personalized ? '70' : '69.8', personalized ? '97.49' : '97.21', { multiplierSource: personalized ? 'user' : 'default' }),
          tokenItem('cache_read', '0.5', '3.59', '0.5', rate46, personalized ? '0.09' : '0.1', personalized ? '3.5' : '3.49', personalized ? '97.49' : '97.21', { multiplierSource: personalized ? 'user' : 'default' }),
        ],
      },
    ]),
    model('gemini-3.1-pro-preview', 'Gemini 3.1 Pro Preview', 'gemini', 'token', sourceGemini, '2026-02-19', [
      {
        group_id: 41,
        billing_mode: 'token',
        price_source: 'litellm',
        items: [
          tokenItem('input', '2', '14.36', '2', '0.1', '0.2', '14.16', '98.61'),
          tokenItem('output', '12', '86.16', '12', '0.1', '1.2', '84.96', '98.61'),
          tokenItem('cache_read', '0.2', '1.436', '0.2', '0.1', '0.02', '1.416', '98.61'),
          tokenItem('input:long_context', '4', '28.72', '4', '0.1', '0.4', '28.32', '98.61', { minContextTokens: 200001 }),
          tokenItem('output:long_context', '18', '129.24', '18', '0.1', '1.8', '127.44', '98.61', { minContextTokens: 200001 }),
        ],
      },
    ]),
    model('grok-4.5', 'Grok 4.5', 'grok', 'token', sourceGrok, '2026-07-08', [
      {
        group_id: 67,
        billing_mode: 'token',
        price_source: 'channel',
        items: [
          tokenItem('input', '2', '14.36', '3', '0.2', '0.6', '13.76', '95.82'),
          tokenItem('output', '6', '43.08', '15', '0.2', '3', '40.08', '93.04'),
          tokenItem('cache_read', '0.3', '2.154', '0.75', '0.2', '0.15', '2.004', '93.04'),
        ],
      },
    ]),
    model('deepseek-v4-flash', 'DeepSeek V4 Flash', 'cn', 'token', sourceDeepSeek, '2026-07-31', [
      {
        group_id: 82,
        billing_mode: 'token',
        price_source: 'channel',
        items: [
          tokenItem('input', '1', '1', '1', '0.2', '0.2', '0.8', '80', { officialCurrency: 'CNY', baseCurrency: 'CNY' }),
          tokenItem('output', '2', '2', '2', '0.2', '0.4', '1.6', '80', { officialCurrency: 'CNY', baseCurrency: 'CNY' }),
          tokenItem('cache_read', '0.02', '0.02', '0.02', '0.2', '0.004', '0.016', '80', { officialCurrency: 'CNY', baseCurrency: 'CNY' }),
        ],
      },
    ]),
  ]
}

export function buildPricingCatalog(personalized = false) {
  const visibleGroups = personalized
    ? groups.filter((group) => mockUser.allowed_groups.includes(group.id))
    : groups
  const visibleIDs = new Set(visibleGroups.map((group) => group.id))
  return {
    generated_at: generatedAt,
    data_version: personalized ? 'demo-personal-20260814' : 'demo-public-20260814',
    stale: false,
    exchange: {
      quota_usd_per_cny: '1',
      usd_cny_reference: '6.8',
      source: '运营固定对比汇率（非实时牌价）',
      effective_date: '2026-08-14',
    },
    groups: visibleGroups.map((group) => groupView(group, personalized)),
    models: buildModels(personalized)
      .map((entry) => ({
        ...entry,
        group_prices: entry.group_prices.filter((price) => visibleIDs.has(price.group_id)),
      }))
      .filter((entry) => entry.group_prices.length > 0),
  }
}

export function apiResponse(data) {
  return JSON.stringify({ code: 0, message: 'success', data })
}
