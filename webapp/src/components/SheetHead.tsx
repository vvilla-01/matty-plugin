import React from 'react';

import {BackIcon, CloseIcon} from './icons';

type Props = {
    title: string;
    note?: string;
    onClose: () => void;
    children?: React.ReactNode;

    // A sheet turned into one step of something it lists (a service's flow
    // in Connected accounts, design/3.4.0.md 5.2): an arrow back to the
    // list, before the title, with its name.
    onBack?: () => void;
    backLabel?: string;

    // The heading's words, focusable by script only, for a sheet that opens
    // onto its words rather than into a field.
    headingRef?: React.MutableRefObject<HTMLSpanElement | null>;
};

// The top of one of the footer's sheets: what it is, a line under that when
// there is something worth saying, anything it can do to itself, and an ×.
// The × is there for pointing and touch; Escape does the same from anywhere
// in a sheet. Its own module so the sheets that live in their own files
// (AccountSheet, TwitchConnect) draw the same head as the ones in
// Sidebar.tsx.
export const SheetHead: React.FC<Props> = ({title, note, onClose, children, onBack, backLabel, headingRef}) => (
    <div className='vntv-sheet-head'>
        {onBack ? (
            <button
                type='button'
                className='vntv-sheet-back'
                aria-label={backLabel || 'Back'}
                title={backLabel || 'Back'}
                onClick={onBack}
            ><BackIcon size={14}/></button>
        ) : null}
        <span
            ref={headingRef}
            className='vntv-sheet-heading'
            tabIndex={headingRef ? -1 : undefined}
        >
            <span className='vntv-sheet-title'>{title}</span>
            {note ? <span className='vntv-sheet-note'>{note}</span> : null}
        </span>
        {children}
        <button
            type='button'
            className='vntv-sheet-close'
            aria-label={`Close ${title}`}
            title='Close'
            onClick={onClose}
        ><CloseIcon size={14}/></button>
    </div>
);
