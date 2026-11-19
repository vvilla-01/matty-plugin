import React from 'react';

// A list that scrolls inside the sheet fades at its foot while there is more
// under it, so a row cut in half reads as more to come rather than as a
// mistake; scrolled to the end, the last row shows whole.
export function useMoreBelow(): {ref: React.MutableRefObject<HTMLDivElement | null>; more: boolean; onScroll: () => void} {
    const ref = React.useRef<HTMLDivElement | null>(null);
    const [more, setMore] = React.useState(false);
    const onScroll = React.useCallback(() => {
        const box = ref.current;
        setMore(box !== null && box.scrollTop + box.clientHeight < box.scrollHeight - 2);
    }, []);

    // After every render: what is in the list changes with the search.
    React.useLayoutEffect(onScroll);

    return {ref, more, onScroll};
}
