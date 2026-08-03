/**
 * What a video item does under a pointer.
 *
 * One tint, in surface-hover, over 150ms — the same answer the sidebar's
 * subscription rows and the queue rows already give, so every item in the app
 * responds the same way. No shadow, ring or scale: the design system rules
 * those out twice, and a transform would shift the grid besides.
 *
 * Shared rather than repeated because it had already drifted three ways. The
 * home card had grown a shadow and a ring, the channel and search cards had
 * nothing at all, and the rails had the tint — three answers to one question,
 * on pages a viewer moves between in a click.
 *
 * The padding is what the tint sits in: a card's children cover its whole box,
 * so without it the colour shows only as slivers between them. The matching
 * negative margin cancels the layout effect, leaving thumbnail widths and grid
 * spacing exactly as they were.
 */
export const videoItemHover =
  '-m-2 rounded-xl p-2 transition-colors duration-150 ease-out hover:bg-surface-hover'
