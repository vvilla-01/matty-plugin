import React from 'react';

import {MINIMUM_CHOICES, minimumChoiceOf, minimumStored, minimumTypedNote} from '../appCheckAdmin';
import type {MinimumChoice} from '../appCheckAdmin';

import {AdminCard} from './adminParts';

// System Console > Plugins > Matty > App check > Minimum Matty version, the
// MinimumAppVersion setting: no minimum, the latest release of Matty, or this
// version or newer, with a box for the version. It stores "", "latest" or
// what is typed in the same key, with the page's Save button, as
// AdminBirthdays does; Mattermost draws the help text under it.
//
// Which choice is selected is this editor's own, taken from the stored value
// when the page opens, so that "This version or newer" stays chosen with its
// box empty, which stores "" until something is typed. A stored value the
// server counts as none is shown as typed under that choice and is never
// replaced until somebody changes it. The rules are appCheckAdmin.ts's,
// which build/check-appcheck.mjs holds.

type Props = {
    id: string;
    label?: string;
    value?: string;
    disabled?: boolean;
    onChange?: (id: string, value: string) => void;
    setSaveNeeded?: () => void;
};

export const AdminMinimum: React.FC<Props> = ({id, label, value, disabled, onChange, setSaveNeeded}) => {
    const [choice, setChoice] = React.useState<MinimumChoice>(() => minimumChoiceOf(value).choice);

    // What the box holds, kept while another choice is selected, so that
    // coming back to this version or newer brings back what was typed.
    const [typed, setTyped] = React.useState(() => minimumChoiceOf(value).typed);

    const box = React.useRef<HTMLInputElement>(null);
    const [focusBox, setFocusBox] = React.useState(false);
    React.useEffect(() => {
        if (focusBox) {
            setFocusBox(false);
            box.current?.focus();
        }
    }, [focusBox]);

    const store = React.useCallback((next: string) => {
        if (onChange) {
            onChange(id, next);
        }
        if (setSaveNeeded) {
            setSaveNeeded();
        }
    }, [id, onChange, setSaveNeeded]);

    const choose = (next: MinimumChoice) => {
        if (next === choice) {
            return;
        }
        setChoice(next);
        store(minimumStored(next, typed));
        if (next === 'version') {
            setFocusBox(true);
        }
    };

    const note = choice === 'version' ? minimumTypedNote(typed) : null;

    return (
        <AdminCard
            id={id}
            label={label}
        >
            <div
                className='vntv-minimum'
                role='radiogroup'
                aria-label={label || undefined}
            >
                {MINIMUM_CHOICES.map((option) => (
                    <label
                        key={option.choice}
                        className='vntv-admin-check'
                    >
                        <input
                            type='radio'
                            name={`${id}-choice`}
                            value={option.choice}
                            checked={choice === option.choice}
                            disabled={disabled}
                            onChange={() => choose(option.choice)}
                        />
                        <span>{option.words}</span>
                    </label>
                ))}
            </div>
            {choice === 'version' ? (
                <div className='vntv-minimum-box'>
                    <input
                        ref={box}
                        className='vntv-input'
                        aria-label='The oldest Matty version this server answers'
                        placeholder='0.1.33'
                        spellCheck={false}
                        autoComplete='off'
                        value={typed}
                        disabled={disabled}
                        onChange={(event) => {
                            setTyped(event.target.value);
                            store(minimumStored('version', event.target.value));
                        }}
                    />
                    {note ? (
                        <p className={note.tone === 'error' ? 'vntv-admin-line vntv-notice-error' : 'vntv-admin-line vntv-admin-quiet'}>{note.text}</p>
                    ) : null}
                </div>
            ) : null}
        </AdminCard>
    );
};
