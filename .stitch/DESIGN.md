---
name: Sub2API Native
colors:
  surface: '#f9fafb'
  surface-dim: '#f1f5f9'
  surface-bright: '#ffffff'
  surface-container-lowest: '#ffffff'
  surface-container-low: '#f8fafc'
  surface-container: '#f1f5f9'
  surface-container-high: '#e2e8f0'
  surface-container-highest: '#cbd5e1'
  on-surface: '#111827'
  on-surface-variant: '#475569'
  inverse-surface: '#0f172a'
  inverse-on-surface: '#f8fafc'
  outline: '#94a3b8'
  outline-variant: '#e2e8f0'
  surface-tint: '#14b8a6'
  primary: '#14b8a6'
  on-primary: '#ffffff'
  primary-container: '#ccfbf1'
  on-primary-container: '#134e4a'
  inverse-primary: '#5eead4'
  secondary: '#475569'
  on-secondary: '#ffffff'
  secondary-container: '#e2e8f0'
  on-secondary-container: '#1e293b'
  tertiary: '#0891b2'
  on-tertiary: '#ffffff'
  tertiary-container: '#cffafe'
  on-tertiary-container: '#164e63'
  error: '#dc2626'
  on-error: '#ffffff'
  error-container: '#fee2e2'
  on-error-container: '#7f1d1d'
  success: '#059669'
  warning: '#d97706'
  background: '#f9fafb'
  on-background: '#111827'
  surface-variant: '#f1f5f9'
  dark-background: '#020617'
  dark-surface: '#0f172a'
  dark-surface-raised: '#1e293b'
  dark-on-surface: '#f1f5f9'
typography:
  page-title:
    fontFamily: system-ui
    fontSize: 48px
    fontWeight: '700'
    lineHeight: 56px
    letterSpacing: -0.02em
  section-title:
    fontFamily: system-ui
    fontSize: 20px
    fontWeight: '600'
    lineHeight: 28px
    letterSpacing: -0.01em
  card-title:
    fontFamily: system-ui
    fontSize: 16px
    fontWeight: '600'
    lineHeight: 24px
    letterSpacing: '0'
  body-base:
    fontFamily: system-ui
    fontSize: 14px
    fontWeight: '400'
    lineHeight: 20px
    letterSpacing: '0'
  body-small:
    fontFamily: system-ui
    fontSize: 12px
    fontWeight: '400'
    lineHeight: 16px
    letterSpacing: '0'
  label-medium:
    fontFamily: system-ui
    fontSize: 12px
    fontWeight: '500'
    lineHeight: 16px
    letterSpacing: 0.01em
  data-mono:
    fontFamily: ui-monospace
    fontSize: 13px
    fontWeight: '500'
    lineHeight: 20px
    letterSpacing: -0.01em
rounded:
  sm: 0.375rem
  DEFAULT: 0.5rem
  md: 0.75rem
  lg: 0.75rem
  xl: 1rem
  2xl: 1rem
  full: 9999px
spacing:
  unit: 4px
  xs: 4px
  sm: 8px
  md: 16px
  lg: 24px
  xl: 32px
  content-desktop: 32px
  content-tablet: 24px
  content-mobile: 16px
  sidebar-expanded: 256px
  sidebar-collapsed: 72px
---

# Design System: Sub2API Native

**Project ID:** 16748391723786967433

## 1. Visual Theme & Atmosphere

Sub2API is a calm, information-dense developer dashboard with a marketplace-style model discovery layer. Its visual language is modern and operational rather than promotional: cool gray surfaces establish hierarchy, teal identifies the active path and primary actions, and restrained gradients add polish without obscuring data. The pricing page borrows the browsing rhythm of a model plaza—hero search, facet sidebar, model-card grid, and detail drawer—while remaining a direct extension of the existing API-key, usage, group, and available-channel views.

The interface uses clear cards, thin borders, compact tables, and small status badges. Whitespace is deliberate but economical. Important monetary values and model identifiers use monospaced numerals for stable alignment. Light and dark modes are equal first-class states; dark mode uses deep Slate layers rather than pure black and keeps teal accents readable without neon glow.

## 2. Color Palette & Roles

### Primary Foundation

- **Cloud Canvas — `#f9fafb`:** light application background.
- **Pure Surface — `#ffffff`:** cards, table bodies, inputs, and the public header.
- **Mist Surface — `#f1f5f9`:** table headers, secondary controls, and nested information bands.
- **Deep Slate Canvas — `#020617`:** dark application background.
- **Slate Surface — `#0f172a`:** dark cards, sidebar, and raised shell surfaces.
- **Raised Slate — `#1e293b`:** dark inputs, selected rows, and nested containers.
- **Hairline Slate — `#e2e8f0`:** light borders and separators.

### Accent & Interactive

- **Sub2API Teal — `#14b8a6`:** active navigation, primary buttons, selected tabs, focus rings, and the most important calculated price.
- **Deep Teal — `#0d9488`:** hover and gradient endpoint for primary actions.
- **Soft Teal — `#ccfbf1`:** selected pills, explanatory badges, and low-emphasis highlights.
- **Cyan Support — `#0891b2`:** secondary informational graphics only; teal remains the dominant accent.

### Typography & Text Hierarchy

- **Ink — `#111827`:** light-mode headings and primary values.
- **Slate Copy — `#475569`:** body copy, labels, and table metadata.
- **Quiet Slate — `#94a3b8`:** timestamps, unavailable comparison values, and helper copy.
- **Snow — `#f1f5f9`:** dark-mode headings and primary values.

### Functional States

- **Success Emerald — `#059669`:** savings, fresh snapshot status, and healthy availability.
- **Warning Amber — `#d97706`:** stale snapshot, future-effective pricing, and partial comparison warnings.
- **Error Red — `#dc2626`:** failed refresh, unavailable catalog, and destructive/error states.

## 3. Typography Rules

### Hierarchy & Weights

- Use the native system sans stack (`system-ui`, Apple system fonts, Segoe UI, Roboto, PingFang SC, Microsoft YaHei) for all interface text.
- Standard application page titles remain 30px/36px at weight 700. The pricing-market hero is the intentional exception: 48px/56px on desktop and 30px/36px on mobile.
- Section headings use 20px/28px at weight 600. Card and model names use 16px/24px at weight 600.
- Body and table copy use 14px/20px. Supporting metadata uses 12px/16px.
- Model IDs, multipliers, exchange rates, timestamps, and all monetary figures use the existing system monospace stack with tabular numerals.
- Keep headings slightly tight; body copy stays neutral and readable. Avoid decorative display typography.

### Spacing Principles

- Text labels sit close to their values, generally 4–8px apart.
- Model row groups use 16–24px internal rhythm so dense data remains scannable.
- Long explanatory copy uses a relaxed 1.5 line height and a constrained measure.

## 4. Component Stylings

### Buttons

- Primary actions use the existing teal gradient, white text, a 12px radius, compact 10px vertical padding, and a restrained teal shadow.
- Secondary buttons use white/Slate surfaces, a hairline border, and no saturated fill.
- Ghost icon buttons rely on a soft gray hover surface. Active press scales to 98% with a 200ms transition.
- All controls retain the existing teal focus ring and visible keyboard focus.

### Cards & Data Containers

- Standard cards use white or translucent Slate surfaces, 16px corners, a subtle 1px border, and the existing low card shadow.
- Explanatory cards may use a very light teal tint or teal left accent, but should not become hero banners.
- Nested price sections use background tone and separators rather than additional heavy cards.
- Hover elevation is reserved for clearly interactive cards; static pricing data remains visually stable.

### Navigation

- Authenticated pages use the existing fixed, collapsible side navigation and top application header. The active item uses a teal-tinted surface and teal icon/text.
- Anonymous pricing uses a compact public header matching `HomeView`: brand left, pricing navigation state, theme/language actions, and login/dashboard action right.
- Desktop discovery uses a sticky left facet sidebar. Request groups appear as full-width selectable rows with model count and effective multiplier; providers appear as compact count chips. Mobile replaces the sidebar with a labeled group select and horizontally scrollable provider chips inside the sticky toolbar.

### Inputs & Forms

- Search and select fields use white/raised-Slate surfaces, 12px corners, hairline borders, and teal border plus ring on focus.
- Minimum interactive height is 40px; mobile touch targets should reach 44px when space allows.
- Placeholder and helper text use quiet Slate, never reduced opacity to the point of poor contrast.

### Pricing Marketplace, Tables, and Detail Drawer

- The default desktop view is a two- or three-column model-card grid. Each interactive card uses a provider mark, monospaced model ID, always-visible actual input/output/cache prices, group badge, effective multiplier, price-source badge, and best valid savings percentage.
- Cards use only subtle hover lift and border tint. The “Details” action opens a right-side drawer rather than navigating away, preserving search and filter context.
- The detail drawer is the authoritative dense view for one model: identity and official reference first, then group/multiplier/billing source summaries, followed by one bordered block per billing item. Official price, system baseline, multiplier, actual RMB charge, and savings are visible together.
- A complete row-grouped table remains available as a secondary desktop view, derived from `AvailableChannelsTable`. Each model begins with a stronger divider and a softly tinted model identity cell; numeric columns align right and use monospaced tabular numerals.
- The actual RMB charge is always the visual anchor. Official price and system baseline remain equally inspectable but lower contrast. Savings uses emerald only when an official one-to-one comparison is valid; “No comparable official SKU” uses neutral copy, never a guessed percentage.
- Mobile uses the same model cards in one column. Full detail opens as a full-width scrollable drawer, so official price and actual charge never depend on hover or a wide table.
- Stale or future-effective data uses an inline badge plus an explicit absolute timestamp.

## 5. Layout Principles

### Grid & Structure

- Authenticated desktop content starts after the 256px expanded or 72px collapsed sidebar and uses the shell’s 32px content padding.
- The pricing page uses the full available dashboard width. Below the hero and billing explanation, desktop content follows a `280px + minmax(0, 1fr)` discovery grid: sticky facets left, toolbar and catalog right.
- The information order is: marketplace hero/status/search, billing explanation, discovery grid, model cards or complete table, model detail drawer.
- The launch catalog excludes the two GPT Image pricing groups and does not expose image-generation models.

### Whitespace Strategy

- Base rhythm is 4px with dominant increments of 8, 16, 24, and 32px.
- Major sections are separated by 24px on desktop and 16px on mobile.
- Cards use 20–24px desktop padding and 16px mobile padding.

### Alignment & Visual Balance

- The marketplace hero is centered; all discovery, card, drawer, and table content is left aligned.
- Numeric values align to the right; model identity and billing labels align left.
- Dense comparison data is balanced by quiet surfaces and consistent column rhythm rather than oversized decoration.

### Responsive Behavior & Touch

- At desktop widths, default to the model-card grid and offer the complete seven-column comparison table as a segmented view option.
- At tablet widths, hide the facet sidebar, keep the group select and provider chips in the sticky toolbar, and retain a two-column card grid.
- Below the mobile breakpoint, use one-column model cards and keep group selection plus provider filters sticky beneath the public/app header. The detail drawer occupies the full viewport width.
- Wide table content may scroll only inside its own container; the document itself must never overflow horizontally at 390, 768, 1280, or 1440px.
- Never rely on hover for official price, actual charge, savings, status, or comparison eligibility.

## 6. Design System Notes for Stitch Generation

### Language to Use

Use “native developer dashboard,” “model marketplace discovery,” “facet sidebar,” “interactive model-card grid,” “right-side pricing detail drawer,” “optional row-grouped comparison table,” “sticky filter controls,” “thin dividers,” “softly rounded cards,” and “monospaced tabular prices.”

### Color References

The design system already applies Sub2API Teal, cool Slate neutrals, the mesh background, and light/dark surfaces. Generation prompts should describe content and structure only and should not restate color or font tokens.

### Component Prompts

- “Create a model marketplace page with a centered search hero, sticky group/provider facet sidebar, count-and-view toolbar, responsive model-card grid, and a right-side detail drawer.”
- “Create a compact billing explanation card showing ¥1 = $1 quota, the operator-fixed 1 USD = ¥6.8 comparison rate, its effective date, the overseas formula, and the native-CNY formula.”
- “Create an interactive model card with monospaced model ID, actual RMB input/output/cache prices, selected group, effective multiplier, price-source badge, savings badge, and an explicit Details action.”
- “Create a pricing detail drawer where each billing item shows official price, system baseline, multiplier, actual RMB charge, and savings in one bordered block.”
- “Keep a row-grouped complete comparison table as a secondary desktop view with aligned tabular numerals and contained horizontal scrolling.”

### Incremental Iteration

Preserve the existing AppLayout shell and control styling. Refine the pricing content area with targeted edits rather than replacing navigation, typography, or visual tokens. Validate desktop light, desktop dark, mobile light, and mobile dark as separate states.
