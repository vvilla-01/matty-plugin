// Where the small player goes, and how big it is, worked out from rectangles
// alone.
//
// Pure, and importing nothing, so the boundaries can be checked by a script
// (build/check-away.mjs) without a browser. MiniPlayer.tsx measures the page
// and hands the numbers in.
//
// It sits in a corner of the message list (#post-list), never over the
// channel header above it, the message box below it or the side panel beside
// it. Three sizes, chosen by the room there is:
//
//   picture: the stream at 16:9 with its name line under it;
//   bar:     one 44px row, with the stream as a thumbnail at its start;
//   hidden:  not even that fits, and it steps aside.

export type Corner = 'tr' | 'tl' | 'br' | 'bl';

export type Rect = {left: number; top: number; right: number; bottom: number};

export type MiniMode = 'picture' | 'bar' | 'hidden';

export type MiniLayout = {mode: MiniMode; x: number; y: number; w: number; h: number};

// The box inside the message list the player keeps to, and its size.
export type MiniBox = {left: number; top: number; right: number; bottom: number; width: number; height: number};

// Clear of the row Mattermost puts its "new messages" and "jump to recent"
// toasts in at the top of the list: a 10px margin and a 32px toast, and room
// to spare. The sides and the bottom only need to keep off the edges.
export const INSET_TOP = 52;
export const INSET_SIDE = 16;
export const INSET_BOTTOM = 12;

// The message list's own scrollbar, when it cannot be measured.
export const DEFAULT_SCROLLBAR = 8;

// The name line under the picture (.vntv-mini-bar, 32px: the player has no
// frame of its own to add), and the whole player in bar mode.
export const NAME_LINE = 32;
export const BAR_HEIGHT = 44;

export const MIN_WIDTH = 240;
export const PICTURE_MAX = 384;
export const BAR_MAX = 360;

// The bar needs this much height: its own 44px and both insets.
export const BAR_ROOM = INSET_TOP + BAR_HEIGHT + INSET_BOTTOM;

const clamp = (low: number, value: number, high: number): number => Math.min(high, Math.max(low, value));

// The message list, less whatever of the side panel lies over it (the
// expanded side panel does) and less its scrollbar.
export function measureRoom(anchor: Rect, rhs: Rect | null, scrollbar: number = DEFAULT_SCROLLBAR): {width: number; height: number; right: number} {
    let right = anchor.right;
    if (rhs && rhs.right > rhs.left && rhs.bottom > rhs.top && rhs.left > anchor.left &&
        rhs.top < anchor.bottom && rhs.bottom > anchor.top) {
        right = Math.min(right, rhs.left);
    }

    return {
        width: Math.max(0, right - anchor.left - Math.max(0, scrollbar)),
        height: Math.max(0, anchor.bottom - anchor.top),
        right,
    };
}

// The box the player keeps to, inset from the message list's edges.
export function boxOf(anchor: Rect, rhs: Rect | null, scrollbar: number = DEFAULT_SCROLLBAR): MiniBox {
    const room = measureRoom(anchor, rhs, scrollbar);
    const left = anchor.left + INSET_SIDE;
    const right = anchor.left + room.width - INSET_SIDE;
    const top = anchor.top + INSET_TOP;
    const bottom = anchor.bottom - INSET_BOTTOM;

    return {left, top, right, bottom, width: right - left, height: bottom - top};
}

// The size for the room there is, and where that puts it in this corner.
export function layoutMini(anchor: Rect, rhs: Rect | null, corner: Corner, scrollbar: number = DEFAULT_SCROLLBAR): MiniLayout {
    const room = measureRoom(anchor, rhs, scrollbar);
    const box = boxOf(anchor, rhs, scrollbar);
    const W = room.width;
    const H = room.height;

    // A picture about a third of the list's width, but never so tall that it
    // takes more than 45 percent of its height.
    let w = clamp(MIN_WIDTH, Math.round(0.30 * W), PICTURE_MAX);
    if ((w * 9 / 16) + NAME_LINE > 0.45 * H) {
        w = Math.floor(((0.45 * H) - NAME_LINE) * 16 / 9);
    }
    if (w >= MIN_WIDTH && W >= w + (2 * INSET_SIDE)) {
        const h = Math.round(w * 9 / 16) + NAME_LINE;

        return {mode: 'picture', w, h, ...cornerPosition(box, w, h, corner)};
    }

    const bar = clamp(MIN_WIDTH, Math.round(0.45 * W), BAR_MAX);
    if (W >= bar + (2 * INSET_SIDE) && H >= BAR_ROOM) {
        return {mode: 'bar', w: bar, h: BAR_HEIGHT, ...cornerPosition(box, bar, BAR_HEIGHT, corner)};
    }

    return {mode: 'hidden', x: 0, y: 0, w: 0, h: 0};
}

export function cornerPosition(box: MiniBox, w: number, h: number, corner: Corner): {x: number; y: number} {
    const x = corner === 'tl' || corner === 'bl' ? box.left : box.right - w;
    const y = corner === 'tl' || corner === 'tr' ? box.top : box.bottom - h;

    return {x: Math.round(x), y: Math.round(y)};
}

// A dragged player stays inside the box.
export function clampPosition(box: MiniBox, w: number, h: number, x: number, y: number): {x: number; y: number} {
    return {
        x: clamp(box.left, x, Math.max(box.left, box.right - w)),
        y: clamp(box.top, y, Math.max(box.top, box.bottom - h)),
    };
}

// Where a dragged player settles when it is let go: the corner its middle is
// nearest.
export function nearestCorner(box: MiniBox, w: number, h: number, x: number, y: number): Corner {
    const left = x + (w / 2) < box.left + (box.width / 2);
    const top = y + (h / 2) < box.top + (box.height / 2);
    if (top) {
        return left ? 'tl' : 'tr';
    }

    return left ? 'bl' : 'br';
}

// The Move button: each press goes one corner on, clockwise.
export function nextCorner(corner: Corner): Corner {
    const order: Corner[] = ['tr', 'br', 'bl', 'tl'];

    return order[(order.indexOf(corner) + 1) % order.length];
}

// An arrow key on the Move button: the corner in that direction, keeping the
// other half. Anything that is not an arrow leaves it where it is.
export function cornerToward(corner: Corner, key: string): Corner {
    const top = corner === 'tl' || corner === 'tr';
    const left = corner === 'tl' || corner === 'bl';
    switch (key) {
    case 'ArrowUp':
        return left ? 'tl' : 'tr';
    case 'ArrowDown':
        return left ? 'bl' : 'br';
    case 'ArrowLeft':
        return top ? 'tl' : 'bl';
    case 'ArrowRight':
        return top ? 'tr' : 'br';
    default:
        return corner;
    }
}

// A remembered corner, or the top right when there is none or it is not one.
export function parseCorner(value: unknown): Corner {
    return value === 'tl' || value === 'bl' || value === 'br' ? value : 'tr';
}

export const CORNER_NAMES: Record<Corner, string> = {
    tr: 'top right',
    tl: 'top left',
    bl: 'bottom left',
    br: 'bottom right',
};
