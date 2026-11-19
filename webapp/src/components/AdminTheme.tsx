import React from 'react';

import {applyThemeToEveryone, fetchTheme} from '../client';

import {AdminCard, AdminHead, AdminPill, AdminStatus, useCardConfirm, useCardFocus} from './adminParts';
import type {AdminCardProps} from './adminParts';

// Applying the theme to everybody who already has an account, from System
// Console > Plugins > Matty.
//
// A button rather than something that happens on save, because it is not a
// settings change: it reaches into every account on the server and replaces a
// choice each of them may have made, and nothing undoes it. So it says so
// above itself, takes a second press, and says afterwards how many people it
// touched.

// The handful worth showing. A swatch for all twenty-six would be a wall of
// colour that says less than five do.
const SHOWN: Array<{key: string; label: string}> = [
    {key: 'sidebarBg', label: 'Sidebar'},
    {key: 'sidebarTextActiveBorder', label: 'Active'},
    {key: 'centerChannelBg', label: 'Messages'},
    {key: 'centerChannelColor', label: 'Text'},
    {key: 'linkColor', label: 'Links'},
    {key: 'buttonBg', label: 'Buttons'},
    {key: 'mentionBg', label: 'Mentions'},
];

export const AdminTheme: React.FC<AdminCardProps> = ({id, label, disabled}) => {
    const [theme, setTheme] = React.useState<Record<string, string> | null>(null);
    const [whitened, setWhitened] = React.useState<string[]>([]);
    const [problem, setProblem] = React.useState('');
    const [busy, setBusy] = React.useState(false);
    const [note, setNote] = React.useState('');
    const [loaded, setLoaded] = React.useState(false);
    const {statusRef, focus} = useCardFocus();
    const confirm = useCardConfirm(() => {
        setNote('');
        setProblem('');
    });

    const load = React.useCallback(() => {
        fetchTheme().then((next) => {
            setLoaded(true);
            setProblem('');
            setWhitened(next.whitened || []);
            try {
                setTheme(next.theme ? JSON.parse(next.theme) : null);
            } catch {
                setTheme(null);
            }
        }).catch((reason: unknown) => {
            setLoaded(true);
            setTheme(null);
            setProblem(reason instanceof Error ? reason.message : String(reason));
        });
    }, []);

    React.useEffect(load, [load]);

    const apply = React.useCallback(() => {
        setBusy(true);
        setNote('');
        setProblem('');

        applyThemeToEveryone().then(({applied}) => {
            setNote(`Done. ${applied} ${applied === 1 ? 'person is' : 'people are'} now on it.`);
        }).catch((reason: unknown) => {
            setProblem(reason instanceof Error ? reason.message : String(reason));
        }).finally(() => setBusy(false));
    }, []);

    return (
        <AdminCard
            id={id}
            label={label}
            focus={focus}
        >
            <AdminHead instant={true}>
                {(() => {
                    if (theme) {
                        return 'The saved theme';
                    }
                    if (!loaded) {
                        return 'Loading…';
                    }

                    return problem === '' ? 'There is no saved theme to apply.' : 'The saved theme could not be read.';
                })()}
            </AdminHead>

            {theme && (
                <div className='vntv-theme-swatches'>
                    {SHOWN.map(({key, label: name}) => (
                        <span
                            key={key}
                            className='vntv-theme-swatch'
                            title={`${key}: ${theme[key] || 'not set'}`}
                        >
                            <span
                                className='vntv-theme-chip'
                                style={{background: theme[key] || 'transparent'}}
                            />
                            <span className='vntv-theme-name'>{name}</span>
                        </span>
                    ))}
                </div>
            )}

            {whitened.length > 0 && (
                <div className='vntv-admin-callout' data-tone='warning'>
                    <p className='vntv-admin-line'>
                        {'Mattermost will turn these white when it saves the theme, because they are '}
                        {'not hex colours: '}<b>{whitened.join(', ')}</b>{'.'}
                    </p>
                </div>
            )}

            {/* Above the button, where it is read before the press: the help
                text Mattermost draws comes after the card. */}
            <p className='vntv-admin-line vntv-admin-quiet vntv-admin-lead'>
                {'Replaces the theme each person has chosen, including any set for one team only. '}
                {'It uses the saved theme, so save the page first.'}
            </p>

            <div className='vntv-admin-actions'>
                <AdminPill
                    off={disabled || !theme}
                    waiting={busy}
                    working={busy}
                    workingWords='Applying…'
                    confirms={true}
                    armed={confirm.armed === 'apply'}
                    onPress={(event) => {
                        if (confirm.press('apply', 'Click again to put everybody on the saved theme. Nobody’s own choice survives it.', event)) {
                            apply();
                        }
                    }}
                >{'Apply to everyone now'}</AdminPill>
                <AdminPill
                    waiting={busy}
                    onPress={load}
                >{'Refresh'}</AdminPill>
            </div>

            <AdminStatus
                ref={statusRef}
                note={confirm.hint || note}
                error={problem}
            />
        </AdminCard>
    );
};
