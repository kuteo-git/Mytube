/**
 * What a video item does under a pointer.
 *
 * One tint, in surface-hover, over 150ms — the same answer the sidebar's
 * subscription rows and the queue rows already give, so every item in the app
 * responds the same way. No shadow, ring or scale: the design system rules
 * those out twice, and a transform would shift the grid besides.
 *
 * Shared rather than repeated because it had already drifted three ways: the
 * home card had grown a shadow and a ring, the channel and search cards had
 * nothing at all, and the rails had the tint.
 */
export const videoItemHover =
  'rounded-xl transition-colors duration-150 ease-out hover:bg-surface-hover'

/**
 * Room for the tint, taken from outside the card rather than inside it.
 *
 * A card's children cover its whole box, so without padding the colour shows
 * only as slivers between them. The matching negative margin gives the space
 * back to the layout, leaving thumbnail widths and grid gaps exactly as they
 * were.
 *
 * A container that clips has to leave room for it: see VideoRail, which pads
 * itself by the page gutter and gives that padding back with a margin, so the
 * overhang has somewhere to go and the cards still line up with the grid.
 */
export const videoItemBleed = '-m-2 p-2'
