import React from 'react';

import {formatDuration} from '../format';
import {PRODUCT_NAME} from '../name';
import {PUSH_SECTION, pushAlertText} from '../pushAdmin';
import {dayDate, viewerZone} from '../time';
import type {LedgerAlert, Me, ReleaseAlert} from '../types';

// What only a system admin is told, in full, in the panel once it is unlocked.
// Never on the lock screen, not even as one line (design/lock-screen.md, the
// amendment of 2026-10-06; its question 13 is superseded): a system admin's
// lock is everybody's, and the System Console, where each of these is put
// right, needs no token. Everything comes with "me", at no cost of its own;
// nothing here for anybody else.
function hasAdminAlerts(me: Me | null): boolean {
    return Boolean(me?.isAdmin && (me.letInAlert || me.voiceAlert || me.birthdayAlert || me.airtimeAlert || me.clipsAlert || me.twitch?.problem ||
        me.steam?.problem || me.hytale?.problem || me.soundAlert || me.achievementsAlert || ledgerLines(me.ledgerAlert).length > 0 ||
        releaseLines(me.releaseAlert) || pushAlertText(me.pushAlert)));
}

// Where in the System Console something is put right: the plugin's page,
// which the console lists by the product's name whatever this server calls
// it, and the section by its title in plugin.json.
export const consolePath = (section: string): string => `System Console > Plugins > ${PRODUCT_NAME} > ${section}`;

// The plugin's files and their signed release (server/release_check.go
// releaseAlert), as one notice: the server's own sentence when they are not
// as signed, or, for a signed release, the files in its folder that nobody
// signed, which Mattermost does not read. null when there is nothing to say.
// where is what follows the files: for files not as signed, the App check
// card, which gives the reason; for unsigned ones, the plugin's folder, since
// the server names at most ten of them and the card none.
function releaseLines(alert: ReleaseAlert | undefined): {text: string; files: string[]; more: number; error: boolean; where: string} | null {
    if (!alert) {
        return null;
    }
    if (alert.problem) {
        return {text: sentence(alert.problem), files: alert.file ? [alert.file] : [], more: 0, error: true, where: ''};
    }

    const count = alert.unlisted || 0;
    if (count <= 0) {
        return null;
    }

    const files = alert.unlistedFiles || [];
    const these = count === 1 ? 'a file' : `${count} files`;

    return {
        text: `This plugin is a signed release, and its folder also holds ${these} nobody signed. Mattermost does not read ${count === 1 ? 'it' : 'them'}, so the release still counts as signed; if nobody here put ${count === 1 ? 'it' : 'them'} there, find out how ${count === 1 ? 'it' : 'they'} got there.`,
        files,
        more: Math.max(0, count - files.length),
        error: false,
        where: count === 1 ? ' Only you can see this. It is in this plugin’s folder on the server: the one file there that release/manifest.txt does not list, apart from the manifest itself and its two signatures.' : ' Only you can see this. They are in this plugin’s folder on the server: every file there that release/manifest.txt does not list, apart from the manifest itself and its two signatures.',
    };
}

// What the fan and 24/7 channel ledgers say (design/3.2.0.md 4.7), one
// sentence each: a ledger that could not be read and was started again from
// zero, on the day it happened. The Airtime card says the same.
export function ledgerLines(alert: LedgerAlert | undefined): string[] {
    if (!alert) {
        return [];
    }

    const zone = viewerZone();
    const lines: string[] = [];
    if (typeof alert.fansResetAt === 'number' && alert.fansResetAt > 0) {
        lines.push(`The biggest-fan hours couldn’t be read on ${dayDate(alert.fansResetAt, zone)}, so they were started again from zero.`);
    }
    if (typeof alert.channelsResetAt === 'number' && alert.channelsResetAt > 0) {
        lines.push(`The 24/7 channel hours couldn’t be read on ${dayDate(alert.channelsResetAt, zone)}, so they were started again from zero.`);
    }

    return lines;
}

export const AdminAlerts: React.FC<{me: Me | null}> = ({me}) => {
    if (!me || !hasAdminAlerts(me)) {
        return null;
    }

    const release = releaseLines(me.releaseAlert);
    const push = pushAlertText(me.pushAlert);

    return (
        <>
            {/* The plugin's own files against their signed release. */}
            {release && (
                <div className={release.error ? 'vntv-notice vntv-notice-error vntv-release-alert' : 'vntv-notice vntv-release-alert'}>
                    {release.text}
                    {release.files.length > 0 && (
                        <ul className='vntv-release-files'>
                            {release.files.map((file) => (
                                <li key={file}><code>{file}</code></li>
                            ))}
                            {release.more > 0 && <li>{`and ${release.more} more`}</li>}
                        </ul>
                    )}
                    {release.where || (
                        <>
                            {' Only you can see this. Look in '}
                            <b>{consolePath('App check')}</b>{'.'}
                        </>
                    )}
                </div>
            )}

            {/* Push notifications are switched on and Mattermost decides to
                push to nobody: the server's words name where Mattermost's own
                are switched on, and the plugin's switch is the other way to
                end it. */}
            {push && (
                <div className='vntv-notice vntv-notice-error'>
                    {push}
                    {' Only you can see this. It goes when Mattermost can push, or when push notifications are switched off in '}
                    <b>{consolePath(PUSH_SECTION)}</b>{'.'}
                </div>
            )}

            {me.letInAlert && (
                <div className='vntv-notice vntv-notice-error'>
                    {sentence(me.letInAlert)}
                    {' Only you can see this. Look in '}
                    <b>{consolePath('Access')}</b>{'.'}
                </div>
            )}

            {me.voiceAlert && (
                <div className='vntv-notice vntv-notice-error'>
                    {'The voice server is not answering: '}{me.voiceAlert}
                    {' Only you can see this, and the panel simply says nothing about voice '}
                    {'rather than claiming it is empty. Put it right in '}
                    <b>{consolePath('Mumble')}</b>{'.'}
                </div>
            )}

            {me.birthdayAlert && (
                <div className='vntv-notice vntv-notice-error'>
                    {'The birthday list could not be read, so nobody is being greeted: '}
                    {me.birthdayAlert}
                    {' Only you can see this. Put it right in '}
                    <b>{consolePath('Birthdays')}</b>{'.'}
                </div>
            )}

            {me.airtimeAlert && (
                <div className='vntv-notice vntv-notice-error'>
                    {me.airtimeAlert.halted ? (
                        <>
                            {'Airtime recording has stopped so the stored history is not overwritten: '}
                            {me.airtimeAlert.halted}
                        </>
                    ) : (
                        <>
                            {'Recorded airtime is '}
                            <b>{formatDuration(me.airtimeAlert.missingSeconds)}</b>
                            {' short of the backup taken when the plugin last started'}
                            {me.airtimeAlert.pluginVersion ? ` (v${me.airtimeAlert.pluginVersion})` : ''}
                            {'.'}
                        </>
                    )}
                    {' Only you can see this. Put it right in '}
                    <b>{consolePath('Airtime and awards')}</b>{'.'}
                </div>
            )}

            {me.clipsAlert && (
                <div className='vntv-notice vntv-notice-error'>
                    {'Clips need attention: '}{sentence(me.clipsAlert)}
                    {' Only you can see this. Put it right in '}
                    <b>{consolePath('Clips')}</b>{'.'}
                </div>
            )}

            {/* Why the plugin's Mumble account cannot play sounds
                (design/3.2.0.md 7.12): the one place a Mumble channel's
                name reaches anybody who is not in it. */}
            {me.soundAlert && (
                <div className='vntv-notice vntv-notice-error'>
                    {sentence(me.soundAlert)}
                    {' Only you can see this. Look in '}
                    <b>{consolePath('Soundboard')}</b>{'.'}
                </div>
            )}

            {/* A line of 24/7 channel achievements that could not be read
                (design/3.2.0.md 5.5). */}
            {me.achievementsAlert && (
                <div className='vntv-notice vntv-notice-error'>
                    {sentence(me.achievementsAlert)}
                    {' Only you can see this. Put it right in '}
                    <b>{consolePath('Airtime and awards')}</b>{'.'}
                </div>
            )}

            {ledgerLines(me.ledgerAlert).map((line) => (
                <div
                    key={line}
                    className='vntv-notice vntv-notice-error'
                >
                    {line}
                    {' Only you can see this.'}
                </div>
            ))}

            {/* What is wrong with Twitch here (design/twitch-live.md 4.10):
                the callback address, the application, Twitch slowing us
                down or out of reach, or its stored data. */}
            {me.twitch?.problem && (
                <div className='vntv-notice vntv-notice-error'>
                    {'Twitch needs attention: '}{sentence(me.twitch.problem)}
                    {' Only you can see this. Look in '}
                    <b>{consolePath('Twitch')}</b>{'.'}
                </div>
            )}

            {/* The same for Steam and Hytale (design/3.4.0.md 2.9): the
                return address, the key or the application, Steam refusing
                the key, slowing us down or out of reach, Hytale refusing the
                application or its scope, or the stored list. A row under
                Connected accounts says so too while Connect is not
                available; this line also covers a problem that leaves
                Connect working, such as Steam refusing the key. */}
            {me.steam?.problem && (
                <div className='vntv-notice vntv-notice-error'>
                    {'Steam needs attention: '}{sentence(me.steam.problem)}
                    {' Only you can see this. Look in '}
                    <b>{consolePath('Steam')}</b>{'.'}
                </div>
            )}
            {me.hytale?.problem && (
                <div className='vntv-notice vntv-notice-error'>
                    {'Hytale needs attention: '}{sentence(me.hytale.problem)}
                    {' Only you can see this. Look in '}
                    <b>{consolePath('Hytale')}</b>{'.'}
                </div>
            )}
        </>
    );
};

// An admin alert as a sentence, ending in a full stop, whatever the server's
// words ended with.
function sentence(words: string): string {
    const trimmed = words.trim();

    return /[.!?…]$/.test(trimmed) ? trimmed : `${trimmed}.`;
}
