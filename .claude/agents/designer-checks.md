# Designer checks

Pass/fail checks for the designer agent, run before the repo's preship. The
repo's `DESIGN.md` and design memory win where they differ. Report every check
that fails or was skipped.

## The bar

- Measure the named reference: open it in the browser and read its computed
  type scale, weights, palette, spacing steps, radii, shadows and motion
  durations. Record them as observations in the repo's design memory (in the
  report when the repo keeps none), then set our own tokens against them.
- A reference is studied, never reused: none of its code, copy, images or
  signature effect ships. Catalogues of brand-derived `DESIGN.md` files are for
  naming a bar, not a system to drop in.
- A new `DESIGN.md` keeps token, rule and reason together in each section:
  theme, colour roles, type scale, components with states, layout and spacing,
  elevation, do and don't, responsive behaviour.

## First screen and content

- The first screen says what this is and who it is for, with one concrete fact
  and one primary action.
- The real thing is shown: product UI, real photographs, real data. No invented
  reviews, quotes, customer logos or figures; a stand-in is listed in the report.
- Headings name a thing. Buttons are a verb and an object.

## One system

- One primary and one secondary button, one card style, one radius scale, one
  icon family at one stroke weight. No emoji as icons.
- Colour and spacing come from tokens: no raw hex or one-off spacing in a
  component. Boxes nest two deep at most.
- Every interactive component defines default, hover, focus-visible, active,
  disabled, loading and error.

## Readable and reachable

- Text contrast 4.5:1; 3:1 for large text, icons and control boundaries.
  Dark mode is measured on its own, not inferred from light.
- Body text 16 px or more on a phone. Lines run 75 characters at most, and 45
  or more where the screen allows it. Tabular figures in number columns.
- Targets 44 by 44 px with 8 px between them. Nothing works on hover only, and
  nothing is told by colour alone.
- The focus ring shows on every control and never sits under a sticky bar.
- Two widths beyond the usual 360 and 1440: no sideways scroll at 320 px, and
  768 px when the column count changes between them.
- Media reserves its space, so nothing shifts as it loads.

## Motion

- Each animation has a purpose it can name: feedback, orientation or
  continuity. Without one it is removed.
- Frequency decides the amount: triggered hundreds of times a day or from the
  keyboard, none; daily, fast and quiet; rare, it may be expressive.
- Movement and resizing animate transform and opacity only, never width,
  height, top, left, margin or padding. Colour may transition on a state change.
- The curve is chosen: arriving decelerates, leaving accelerates, no bare
  `ease`. Bounce only on a playful product, never on a menu, toggle or modal of
  a work tool.
- Work tools stay under 300 ms; marketing and consumer pages 200 to 500 ms.
  Small elements move faster than large ones, and an exit is shorter and
  smaller than its enter (about two thirds).
- Nothing grows from `scale(0)`: start at 0.9 or more with opacity. Menus and
  popovers grow from their trigger.
- Anything the user can re-trigger is a transition, so it can change direction
  mid-flight. Input is never blocked by an animation.
- Generic-motion tells, each a fail: the same entrance on four or more distinct
  components in a view; hover-scale on three or more component types; stagger
  on two or more lists in a view; an entrance on headings, body text or nav; an
  endless pulse or glow on a status element; parallax on text; scroll-jacking.
- Under `prefers-reduced-motion` everything rests in its final state and
  nothing stays hidden. A loop longer than 5 s has a pause control.

## Sources

Ideas only, rewritten in our own words; nothing here is copied or installed.
All five repos are MIT.

- [Leonxlnx/taste-skill](https://github.com/Leonxlnx/taste-skill): first
  screen, real material, one system, scroll-motion bans.
- [kylezantos/design-motion-principles](https://github.com/kylezantos/design-motion-principles):
  frequency gate and generic-motion tells, after the published work of Emil
  Kowalski, Jakub Krehel and Jhey Tompkins.
- [nextlevelbuilder/ui-ux-pro-max-skill](https://github.com/nextlevelbuilder/ui-ux-pro-max-skill):
  accessibility, touch and type checks, in that priority order.
- [VoltAgent/awesome-claude-design](https://github.com/VoltAgent/awesome-claude-design):
  the `DESIGN.md` section set; a catalogue for studying a bar.
- [bergside/design-md-chrome](https://github.com/bergside/design-md-chrome):
  measuring a reference's computed styles; the component state list.
- [WCAG 2.2](https://www.w3.org/TR/WCAG22/): contrast, target size and pause
  thresholds.
