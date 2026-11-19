import React from 'react';

import manifestJson from '../../plugin.json';
import {ADMIN_SETTINGS} from '../src/adminSettings';

// System Console > Plugins > Matty, the whole page, the way Mattermost draws
// it from plugin.json: the header, one white card per section with its title
// and subtitle, Bootstrap's label column and control column, the help text
// under each control, and the plugin's own components in their places. Lab
// only; the plugin never imports this.
//
// Built from Mattermost's own source rather than from memory (v10.11.0,
// whose admin console styles master keeps, v11.11.1 and master; see the
// notes beside each rule), because the point is
// to look at the real page before it ships: how long it is, what reads as a
// wall of text, and whether the plugin's cards sit in it like Mattermost's own
// controls. What it cannot copy is the typeface: Open Sans and Metropolis are
// not inlined, so the nearest installed sans stands in.
//
// The plugin supports Mattermost 11.11 only, and the bar along the top switches
// between it and unreleased master, which lays the page out differently:
//   * 11.11: the plugin's header and footer sit around the Enable row, at the
//     top of the page, above the first section, under a line naming the
//     plugin; a custom setting's help text is markdown;
//   * master: no header at all, the footer last, a card with the description
//     and an Enable toggle in place of the Enable row.

type Option = {display_name: string; value: string};

type SchemaSetting = {
    key: string;
    display_name?: string;
    type: string;
    help_text?: string;
    placeholder?: string;
    default?: unknown;
    options?: Option[];
    secret?: boolean;
};

type SchemaSection = {
    key: string;
    title?: string;
    subtitle?: string;
    header?: string;
    footer?: string;
    settings: SchemaSetting[];
};

type Manifest = {
    id: string;
    name: string;
    version: string;
    description: string;
    homepage_url?: string;
    settings_schema: {
        header?: string;
        footer?: string;
        settings?: SchemaSetting[];
        sections?: SchemaSection[];
    };
};

const manifest = manifestJson as unknown as Manifest;

export type MattermostVersion = '11.11' | 'master';

const VERSIONS: Array<{version: MattermostVersion; note: string}> = [
    {version: '11.11', note: '11.11: header and footer around the Enable row, markdown in custom help, the plugin line over the form'},
    {version: 'master', note: 'Unreleased master: no header, the footer last, the plugin card with its Enable toggle'},
];

// Mattermost's key for a plugin setting: PluginSettings.Plugins.<id with + for
// .>.<lowercased key> (custom_plugin_settings/index.ts).
const escapedId = manifest.id.replace(/\./g, '+');

// What Mattermost puts in place of a secret it will not send to the browser
// (model.FakeSetting). The server swaps every setting the plugin's manifest
// marks secret for it.
const FAKE_SETTING = '********************************';
const configKey = (key: string): string => `PluginSettings.Plugins.${escapedId}.${key.toLowerCase()}`;

// ---- Markdown, the subset Mattermost's marked fork renders -----------------
//
// schema_text.tsx: marked(text, {breaks: true, sanitize: true}) with a renderer
// whose paragraph() returns the text bare. So a blank line between paragraphs
// glues them together with nothing between, a single newline is a <br>, raw
// HTML is escaped, and a list keeps the newlines that pre-line then shows as
// blank lines.

const escapeHTML = (text: string): string => text.
    replace(/&/g, '&amp;').
    replace(/</g, '&lt;').
    replace(/>/g, '&gt;').
    replace(/"/g, '&quot;').
    replace(/'/g, '&#39;');

const link = (href: string, text: string): string =>
    `<a href="${escapeHTML(href)}" target="_blank" rel="noopener noreferrer">${text}</a>`;

function inline(source: string): string {
    const held: string[] = [];
    const hold = (html: string): string => {
        held.push(html);

        return `\u0000${held.length - 1}\u0000`;
    };

    let text = source.replace(/(`+)([\s\S]*?[^`])\1(?!`)/g, (_, _ticks: string, code: string) => hold(`<code>${escapeHTML(code.trim())}</code>`));
    text = text.replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, (_, words: string, href: string) => hold(link(href, escapeHTML(words))));
    text = text.replace(/https?:\/\/[^\s<]+[^<.,:;"')\]\s]/g, (href: string) => hold(link(href, escapeHTML(href))));
    text = escapeHTML(text);
    text = text.replace(/\*\*([\s\S]+?)\*\*(?!\*)/g, '<strong>$1</strong>');
    text = text.replace(/(^|[^\w])__([\s\S]+?)__(?=[^\w]|$)/g, '$1<strong>$2</strong>');
    text = text.replace(/\*([^*\s][\s\S]*?)\*(?!\*)/g, '<em>$1</em>');
    text = text.replace(/(^|[^\w])_([^_\s][\s\S]*?)_(?=[^\w]|$)/g, '$1<em>$2</em>');
    text = text.replace(/ *\n/g, '<br>');

    return text.replace(/\u0000(\d+)\u0000/g, (_, at: string) => held[Number(at)]);
}

export function markdown(source: string): string {
    return source.replace(/\r\n?/g, '\n').trim().split(/\n{2,}/).map((block) => {
        const lines = block.split('\n');
        if (lines.every((line) => (/^\s*([-*+]|\d+\.)\s+/).test(line))) {
            const tag = (/^\s*\d+\./).test(lines[0]) ? 'ol' : 'ul';
            const items = lines.map((line) => `<li>${inline(line.replace(/^\s*([-*+]|\d+\.)\s+/, ''))}</li>\n`).join('');

            return `<${tag}>\n${items}</${tag}>\n`;
        }

        const heading = (/^(#{1,6})\s+(.*)$/).exec(block);
        if (heading && lines.length === 1) {
            return `<h${heading[1].length}>${inline(heading[2])}</h${heading[1].length}>\n`;
        }

        return inline(block);
    }).join('');
}

const Markdown: React.FC<{text: string}> = ({text}) => (
    <span dangerouslySetInnerHTML={{__html: markdown(text)}}/>
);

// ---- The page --------------------------------------------------------------

type Values = Record<string, unknown>;

function defaultsOf(): Values {
    const values: Values = {};
    for (const section of manifest.settings_schema.sections || []) {
        for (const setting of section.settings) {
            if (setting.default !== undefined) {
                values[setting.key.toLowerCase()] = setting.default;
            }
        }
    }

    return values;
}

const registered = new Map(ADMIN_SETTINGS.map((setting) => [setting.key, setting]));

// The custom settings the plugin registers that plugin.json does not list,
// which Mattermost would never draw: shown at the end of the lab's page, in
// a section of their own, so a card can be looked at before its row lands in
// plugin.json — as Twitch connections could while the server half of 3.1.0
// was written beside it — and so a row that went missing is seen.
const listedKeys = new Set([
    ...(manifest.settings_schema.settings || []),
    ...(manifest.settings_schema.sections || []).flatMap((section) => section.settings),
].map((setting) => setting.key.toLowerCase()));

const UNLISTED_NAMES: Record<string, string> = {
    twitchsetup: 'Twitch connections',
    steamsetup: 'Steam connections',
    hytalesetup: 'Hytale connections',
};

const unlisted: SchemaSetting[] = ADMIN_SETTINGS.
    filter((setting) => !listedKeys.has(setting.key)).
    map((setting) => ({key: setting.key, display_name: UNLISTED_NAMES[setting.key] || setting.key, type: 'custom'}));

type PageProps = {
    // Seeds for the page, as a real server's saved configuration would hold
    // them: the lab's long birthday list, for one.
    seed?: Values;
    onExit: () => void;
};

export const ConsolePage: React.FC<PageProps> = ({seed, onExit}) => {
    const [version, setVersion] = React.useState<MattermostVersion>('11.11');
    const [readOnly, setReadOnly] = React.useState(false);
    const [full, setFull] = React.useState(false);

    // A dark console: Onyx's variables on the page, for cards that take
    // their colours from them. Mattermost draws its System Console in Denim
    // whatever the admin's theme; this is for looking, not a copy of a page.
    const [dark, setDark] = React.useState(false);
    const [values, setValues] = React.useState<Values>(() => ({...defaultsOf(), ...seed}));
    const [saved, setSaved] = React.useState<Values>(values);
    const [dirty, setDirty] = React.useState(false);

    const change = React.useCallback((key: string, value: unknown) => {
        setValues((current) => ({...current, [key.toLowerCase()]: value}));
        setDirty(true);
    }, []);

    // What System Console hands every custom setting as config: the SAVED
    // configuration, keys lowercased, never what is typed but unsaved.
    const config = React.useMemo(() => ({PluginSettings: {Plugins: {[manifest.id]: saved}}}), [saved]);

    const schema = manifest.settings_schema;

    const helpText = (text: string | undefined) => (
        <div className='help-text'>
            <Markdown text={text || ''}/>
        </div>
    );

    const renderSetting = (setting: SchemaSetting) => {
        const id = configKey(setting.key);
        const lowered = setting.key.toLowerCase();
        const value = values[lowered];
        const label = setting.display_name || setting.key;

        if (setting.type === 'custom') {
            const custom = registered.get(lowered);
            if (!custom) {
                return (
                    <div
                        key={setting.key}
                        className='banner warning'
                    >{'In order to view this setting, enable the plugin and click Save.'}</div>
                );
            }

            const Component = custom.component;
            const instance = (
                <Component
                    key={`custom_${setting.key}`}
                    id={id}
                    label={label}
                    value={value}
                    disabled={readOnly}
                    config={config}
                    onChange={(_: string, next: unknown) => change(setting.key, next)}
                    setSaveNeeded={() => setDirty(true)}
                    registerSaveAction={() => undefined}
                    unRegisterSaveAction={() => undefined}
                    cancelSubmit={() => undefined}
                    showConfirm={false}
                />
            );

            if (!custom.showTitle) {
                return instance;
            }

            return (
                <div
                    key={setting.key}
                    data-testid={id}
                    className='form-group'
                >
                    <label
                        className='control-label col-sm-4'
                        htmlFor={id}
                    >{label}</label>
                    <div className='col-sm-8'>
                        {instance}
                        {helpText(setting.help_text)}
                    </div>
                </div>
            );
        }

        if (setting.type === 'bool') {
            const on = value === true || value === 'true';

            return (
                <fieldset
                    key={setting.key}
                    id={id}
                    className='form-group'
                >
                    <legend className='control-label form-legend col-sm-4'>{label}</legend>
                    <div className='col-sm-8'>
                        {[true, false].map((choice) => (
                            <label
                                key={String(choice)}
                                className='lab-mm-radio'
                            >
                                <input
                                    type='radio'
                                    name={id}
                                    checked={on === choice}
                                    disabled={readOnly}
                                    onChange={() => change(setting.key, choice)}
                                />
                                {choice ? 'True' : 'False'}
                            </label>
                        ))}
                        {helpText(setting.help_text)}
                    </div>
                </fieldset>
            );
        }

        let control: React.ReactNode;
        if (setting.type === 'dropdown') {
            control = (
                <select
                    id={id}
                    className='form-control'
                    value={String(value ?? setting.options?.[0]?.value ?? '')}
                    disabled={readOnly}
                    onChange={(event) => change(setting.key, event.target.value)}
                >
                    {(setting.options || []).map((option) => (
                        <option
                            key={option.value}
                            value={option.value}
                        >{option.display_name}</option>
                    ))}
                </select>
            );
        } else if (setting.type === 'longtext') {
            control = (
                <textarea
                    id={id}
                    className='form-control'
                    rows={5}
                    dir='auto'
                    placeholder={setting.placeholder}
                    value={String(value ?? '')}
                    disabled={readOnly}
                    onChange={(event) => change(setting.key, event.target.value)}
                />
            );
        } else {
            // The lab saves a made-up proxy password, so the mask shows.
            const shown = setting.secret && String(value ?? '') !== '' ? FAKE_SETTING : String(value ?? '');
            control = (
                <input
                    id={id}
                    className='form-control'
                    type={setting.type === 'number' ? 'number' : 'text'}
                    placeholder={setting.placeholder}
                    value={shown}
                    disabled={readOnly}
                    onChange={(event) => change(setting.key, setting.type === 'number' ? parseInt(event.target.value, 10) : event.target.value)}
                />
            );
        }

        return (
            <div
                key={setting.key}
                data-testid={id}
                className='form-group'
            >
                <label
                    className='control-label col-sm-4'
                    htmlFor={id}
                >{label}</label>
                <div className='col-sm-8'>
                    {control}
                    {helpText(setting.help_text)}
                </div>
            </div>
        );
    };

    const banner = (text: string | undefined) => (text ? (
        <div className='banner'><Markdown text={text}/></div>
    ) : null);

    const enableRow = (
        <fieldset
            className='form-group'
            id={`PluginSettings.PluginStates.${escapedId}.Enable`}
        >
            <legend className='control-label form-legend col-sm-4'>{'Enable Plugin: '}</legend>
            <div className='col-sm-8'>
                <label className='lab-mm-radio'>
                    <input
                        type='radio'
                        name='lab-mm-enable'
                        defaultChecked={true}
                        disabled={readOnly}
                    />
                    {'True'}
                </label>
                <label className='lab-mm-radio'>
                    <input
                        type='radio'
                        name='lab-mm-enable'
                        disabled={readOnly}
                    />
                    {'False'}
                </label>
                <div className='help-text'>{'When true, this plugin is enabled.'}</div>
            </div>
        </fieldset>
    );

    return (
        <div
            className='lab-mm'
            data-full={full ? 'true' : undefined}
            data-dark={dark ? 'true' : undefined}
            data-version={version}
        >
            <style>{MM_CSS}</style>
            <div className='lab-mm-global'>
                <button
                    type='button'
                    className='lab-mm-chip'
                    onClick={onExit}
                >{'Back to the lab'}</button>
                <span className='lab-mm-bar-label'>{'Mattermost'}</span>
                {VERSIONS.map((option) => (
                    <button
                        key={option.version}
                        type='button'
                        className='lab-mm-chip'
                        aria-pressed={version === option.version}
                        title={option.note}
                        onClick={() => setVersion(option.version)}
                    >{option.version}</button>
                ))}
                <button
                    type='button'
                    className='lab-mm-chip'
                    aria-pressed={readOnly}
                    title='An admin who may read the plugin settings but not change them, or the plugin disabled: Mattermost passes disabled to every control'
                    onClick={() => setReadOnly(!readOnly)}
                >{'read-only'}</button>
                <button
                    type='button'
                    className='lab-mm-chip'
                    aria-pressed={full}
                    title='The whole page at once, for screenshots: nothing scrolls'
                    onClick={() => setFull(!full)}
                >{'full length'}</button>
                <button
                    type='button'
                    className='lab-mm-chip'
                    aria-pressed={dark}
                    title='The page on Onyx’s variables, dark, to look at the cards on a dark console'
                    onClick={() => setDark(!dark)}
                >{'dark'}</button>
                <span className='lab-mm-bar-note'>{'Lab: Export, Back up and the uploads use the lab’s fake server; nothing leaves this page.'}</span>
            </div>
            <nav className='lab-mm-side'>
                <div className='lab-mm-side-head'>{'System Console'}</div>
                <div className='lab-mm-side-group'>{'Plugins'}</div>
                <div className='lab-mm-side-item'>{'Plugin Management'}</div>
                <div className='lab-mm-side-item'>{'Calls'}</div>
                <div
                    className='lab-mm-side-item'
                    aria-current='page'
                >{manifest.name}</div>
            </nav>

            <div
                className='admin-console__wrapper admin-console'
                id='adminConsoleWrapper'
            >
                <div className='wrapper--fixed'>
                    <div className='admin-console__header'>
                        {manifest.name}
                        {version === 'master' ? (
                            <span className='lab-mm-badge'>{'✓ Running'}</span>
                        ) : null}
                    </div>
                    <div className='admin-console__wrapper lab-mm-scroll'>
                        <div className='admin-console__content'>
                            {version === '11.11' ? (
                                <div className='lab-mm-metaline'>
                                    {/* 11.11's plugin_metadata_panel.tsx: the name links to
                                        homepage_url. Master passes hideName, so its card has
                                        no name and so no link. */}
                                    {manifest.homepage_url ? (
                                        <a
                                            href={manifest.homepage_url}
                                            target='_blank'
                                            rel='noopener noreferrer'
                                        ><b>{manifest.name}</b></a>
                                    ) : <b>{manifest.name}</b>}
                                    {` (${manifest.id} - v${manifest.version})`}
                                </div>
                            ) : null}
                            {version === 'master' ? (
                                <div className='lab-mm-metacard'>
                                    <div className='lab-mm-metacard-row'>
                                        <div className='lab-mm-metacard-id'>
                                            <span className='lab-mm-metacard-name'>{manifest.id}</span>
                                            <span className='lab-mm-metacard-meta'>{`Version ${manifest.version}`}</span>
                                        </div>
                                        <label className='lab-mm-toggle'>
                                            {'Enable plugin'}
                                            <span
                                                className='lab-mm-toggle-track'
                                                aria-hidden={true}
                                            />
                                        </label>
                                    </div>
                                    <p className='lab-mm-metacard-desc'>{manifest.description}</p>
                                </div>
                            ) : null}
                            <form
                                className='form-horizontal'
                                role='form'
                                onSubmit={(event) => event.preventDefault()}
                            >
                                <div>
                                    {version === 'master' ? null : (
                                        <div><div>
                                            {banner(schema.header)}
                                            {enableRow}
                                            {banner(schema.footer)}
                                        </div></div>
                                    )}
                                    {(schema.sections || []).map((section) => (
                                        <div
                                            key={section.key}
                                            className='config-section'
                                            data-section={section.key}
                                        >
                                            <div className='admin-console__wrapper'>
                                                <div className='admin-console__content'>
                                                    <div className='section-header'>
                                                        <div className='section-title'>{section.title}</div>
                                                        <div className='section-subtitle'>{section.subtitle}</div>
                                                    </div>
                                                    <div className='section-body'>
                                                        {banner(section.header)}
                                                        {section.settings.map(renderSetting)}
                                                        {banner(section.footer)}
                                                    </div>
                                                </div>
                                            </div>
                                        </div>
                                    ))}
                                    {unlisted.length > 0 ? (
                                        <div
                                            className='config-section'
                                            data-section='lab-unlisted'
                                        >
                                            <div className='admin-console__wrapper'>
                                                <div className='admin-console__content'>
                                                    <div className='section-header'>
                                                        <div className='section-title'>{'Not in plugin.json (lab only)'}</div>
                                                        <div className='section-subtitle'>
                                                            {'Custom settings adminSettings.ts registers that plugin.json does not list. Mattermost would draw none of them; the lab draws them here so they can be looked at.'}
                                                        </div>
                                                    </div>
                                                    <div className='section-body'>
                                                        {unlisted.map(renderSetting)}
                                                    </div>
                                                </div>
                                            </div>
                                        </div>
                                    ) : null}
                                    {version === 'master' ? banner(schema.footer) : null}
                                </div>
                            </form>
                        </div>
                    </div>
                    <div className='admin-console-save'>
                        <button
                            type='button'
                            id='saveSetting'
                            className='btn btn-primary'
                            disabled={!dirty}
                            onClick={() => {
                                setSaved(values);
                                setDirty(false);
                            }}
                        >{'Save'}</button>
                    </div>
                </div>
            </div>
        </div>
    );
};

// Mattermost's rules, scoped to the replica so none of them reach the lab
// around it. Scoped with :where(), which adds nothing to a selector's
// specificity, so each rule meets the plugin's at exactly the strength it has in
// Mattermost: .admin-console h4 still beats a single class, as it does there. Bootstrap 3.4.1 where Mattermost relies on it (the grid, the
// labels, the inputs, p, h4, label, code), then sass/routes/_admin-console.scss
// and schema_admin_settings.scss at v10.11.0, which master keeps. Pixel values
// throughout: Mattermost's html is 10px, the lab's is not.
const MM_CSS = `
.lab-mm {
    --center-channel-bg: #ffffff;
    --center-channel-bg-rgb: 255, 255, 255;
    --center-channel-color: #3f4350;
    --center-channel-color-rgb: 63, 67, 80;
    --button-bg: #1c58d9;
    --button-bg-rgb: 28, 88, 217;
    --button-color: #ffffff;
    --link-color: #386fe5;
    --link-color-rgb: 56, 111, 229;
    --error-text: #d24b4e;
    --error-text-color-rgb: 210, 75, 78;
    --online-indicator: #3db887;
    --online-indicator-rgb: 61, 184, 135;
    --away-indicator: #ffbc1f;
    --away-indicator-rgb: 255, 188, 31;
    --radius-xs: 2px;
    --radius-s: 4px;
    --elevation-1: 0 2px 3px 0 rgba(0, 0, 0, 0.08);

    position: fixed;
    inset: 0;
    z-index: 200;
    display: grid;
    grid-template: "global global" 40px "side console" 1fr / 220px 1fr;
    background: rgb(245, 245, 245);
    color: #333333;
    font: 14px/1.42857143 'Open Sans', 'Noto Sans', sans-serif;
    -webkit-font-smoothing: antialiased;
    color-scheme: light;
}
.lab-mm[data-full="true"] { position: absolute; bottom: auto; min-height: 100%; grid-template-rows: 40px auto; }
:where(.lab-mm) *, :where(.lab-mm) *::before, :where(.lab-mm) *::after { box-sizing: border-box; }
:where(.lab-mm) *:focus { outline: none; }
:where(.lab-mm) b, :where(.lab-mm) strong { font-weight: 600; }
:where(.lab-mm) a { color: #1c58d9; text-decoration: none; word-break: break-word; cursor: pointer; }
:where(.lab-mm) p { margin: 0 0 10px; }
:where(.lab-mm) label { display: inline-block; max-width: 100%; margin-bottom: 5px; font-weight: 700; }
:where(.lab-mm) h1, :where(.lab-mm) h2, :where(.lab-mm) h3 { font-family: Metropolis, 'Noto Sans', sans-serif; }
:where(.lab-mm) h3, :where(.lab-mm) h4 { margin-top: 10px; margin-bottom: 10px; font-weight: 500; line-height: 1.1; }
:where(.lab-mm) h3 { font-size: 24px; }
:where(.lab-mm) h4 { font-size: 18px; }
:where(.lab-mm) ul, :where(.lab-mm) ol { padding-left: 22px; margin-top: 3px; margin-bottom: 0.11em; }
:where(.lab-mm) code { padding: 2px 4px; font-size: 90%; border-radius: 4px; font-family: Menlo, Monaco, Consolas, 'Courier New', monospace;
    background-color: rgba(var(--center-channel-color-rgb), 0.1); color: inherit; }
:where(.lab-mm) img { max-width: 100%; vertical-align: middle; }
:where(.lab-mm) button, :where(.lab-mm) input, :where(.lab-mm) select, :where(.lab-mm) textarea { font-family: inherit; font-size: inherit; line-height: inherit; margin: 0; }
:where(.lab-mm) button { overflow: visible; text-transform: none; cursor: pointer; }
:where(.lab-mm) input[type="file"] { display: block; }
:where(.lab-mm) input[type="radio"], :where(.lab-mm) input[type="checkbox"] { margin: 4px 0 0; line-height: normal; }
:where(.lab-mm) fieldset { min-width: 0; padding: 0; margin: 0; border: 0; }
:where(.lab-mm) legend { display: block; width: 100%; padding: 0; margin-bottom: 20px; font-size: 21px; line-height: inherit; border: 0; }
:where(.lab-mm) div[disabled] { opacity: 0.7; pointer-events: none; }

.lab-mm-global { grid-area: global; display: flex; align-items: center; gap: 8px; padding: 0 12px; background: #14213e; color: #ffffff; font-size: 12px; }
.lab-mm-chip { padding: 2px 9px; border: 1px solid rgba(255, 255, 255, 0.24); border-radius: 999px; background: transparent; color: #ffffff; font-size: 12px; }
.lab-mm-chip[aria-pressed="true"] { border-color: #ffffff; background: rgba(255, 255, 255, 0.16); }
.lab-mm-bar-label { margin-left: 12px; opacity: 0.64; }
.lab-mm-bar-note { margin-left: auto; opacity: 0.64; }
.lab-mm-side { grid-area: side; background: #1e325c; color: rgba(255, 255, 255, 0.72); padding: 12px 0; font-size: 14px; }
.lab-mm-side-head { padding: 4px 20px 16px; color: #ffffff; font-weight: 600; }
.lab-mm-side-group { padding: 8px 20px 4px; font-size: 12px; font-weight: 600; letter-spacing: 0.02em; text-transform: uppercase; opacity: 0.64; }
.lab-mm-side-item { padding: 6px 20px 6px 32px; }
.lab-mm-side-item[aria-current="page"] { background: rgba(255, 255, 255, 0.08); color: #ffffff; box-shadow: inset 4px 0 0 #5d89ea; }

:where(.lab-mm) .admin-console__wrapper { overflow: auto; }
:where(.lab-mm) .admin-console { grid-area: console; overflow: auto; height: 100%; color: var(--center-channel-color); scroll-behavior: smooth; }
:where(.lab-mm) .admin-console .wrapper--fixed { display: flex; height: 100%; flex-direction: column; }
:where(.lab-mm) .admin-console .admin-console__wrapper { position: relative; height: 100%; flex: 1 1 auto; padding: 20px; }
:where(.lab-mm) .admin-console .admin-console__content { max-width: 920px; }
:where(.lab-mm) .admin-console .admin-console__header { z-index: 100; display: flex; height: 64px; flex: 0 0 64px; align-items: center; gap: 12px;
    padding: 12px 20px; border-bottom: 1px solid rgba(0, 0, 0, 0.1); background: #ffffff;
    font-family: Metropolis, 'Noto Sans', sans-serif; font-size: 22px; font-weight: 600; letter-spacing: -0.01em; line-height: 32px; }
:where(.lab-mm) .admin-console .admin-console-save { z-index: 100; display: flex; width: 100%; height: 60px; flex: 0 0 60px; align-items: center;
    padding: 0 10px; border-top: 1px solid rgba(0, 0, 0, 0.1); background: #ffffff; }
:where(.lab-mm[data-full="true"]) .admin-console, :where(.lab-mm[data-full="true"]) .admin-console .wrapper--fixed,
:where(.lab-mm[data-full="true"]) .admin-console .lab-mm-scroll { height: auto; overflow: visible; }
:where(.lab-mm) .btn { display: inline-block; height: 40px; min-width: 100px; margin: 0 10px; padding: 0 20px; border: 1px solid transparent;
    border-radius: var(--radius-s); font-size: 14px; font-weight: 600; line-height: 38px; }
:where(.lab-mm) .btn-primary { background: rgb(var(--button-bg-rgb)); color: #ffffff; }
:where(.lab-mm) .btn-primary:disabled { opacity: 0.65; cursor: not-allowed; }

:where(.lab-mm) .admin-console h3 { padding-bottom: 0.5em; margin: 1em 0; font-weight: 600; }
:where(.lab-mm) .admin-console h4 { margin-bottom: 2em; font-weight: 600; }
:where(.lab-mm) .admin-console mark { border-radius: 5%; background-color: #fcea8d; }
:where(.lab-mm) .admin-console .form-horizontal .control-label { padding-right: 0; font-weight: 600; text-align: left; }
:where(.lab-mm) .admin-console .form-horizontal .help-text { white-space: pre-line; }
:where(.lab-mm) .admin-console .form-horizontal .form-group { margin-bottom: 25px; }
:where(.lab-mm) .admin-console .form-control { border: 1px solid rgba(0, 0, 0, 0.15); background-color: #ffffff; }
:where(.lab-mm) .admin-console .form-control:focus { border-color: #66afe9; box-shadow: inset 0 1px 1px rgba(0, 0, 0, 0.075), 0 0 8px rgba(102, 175, 233, 0.75); outline: 0; }
:where(.lab-mm) .admin-console .banner { padding: 0.8em 15px; border: 1px solid rgba(var(--center-channel-color-rgb), 0.12); margin: 4px 0 2em;
    background: #ffffff; font-size: 0.95em; }
:where(.lab-mm) .admin-console .banner code { background: rgb(245, 245, 245); }
:where(.lab-mm) .admin-console .banner.warning { border-color: #faebcc; background: #fcf8e3; color: #8a6d3b; }

:where(.lab-mm) .form-horizontal .form-group { margin-right: -15px; margin-left: -15px; }
:where(.lab-mm) .form-horizontal .form-group::before, :where(.lab-mm) .form-horizontal .form-group::after { display: table; content: " "; }
:where(.lab-mm) .form-horizontal .form-group::after { clear: both; }
:where(.lab-mm) .form-group { max-width: 100%; margin-bottom: 15px; }
:where(.lab-mm) .form-group .control-label { overflow: hidden; padding-right: 5px; text-overflow: ellipsis; }
:where(.lab-mm) .col-sm-4, :where(.lab-mm) .col-sm-8 { position: relative; min-height: 1px; padding-right: 15px; padding-left: 15px; }
/* Bootstrap's: the label beside its control only from 768px, stacked above it
   below. The console keeps its 220px sidebar at every width, so at 800px the
   control column is under 300px: the narrow case worth looking at. */
@media (min-width: 768px) {
    :where(.lab-mm) .col-sm-4, :where(.lab-mm) .col-sm-8 { float: left; }
    :where(.lab-mm) .col-sm-4 { width: 33.33333333%; }
    :where(.lab-mm) .col-sm-8 { width: 66.66666667%; }
    :where(.lab-mm) .form-horizontal .control-label { padding-top: 7px; margin-bottom: 0; }
}
:where(.lab-mm) .form-legend { border: none; margin: 0 0 8px; color: inherit; font-size: inherit; font-weight: 600; }
:where(.lab-mm) .form-control { display: block; width: 100%; height: 34px; padding: 6px 12px; font-size: 14px; line-height: 1.42857143; color: #555555;
    border-radius: var(--radius-xs); box-shadow: inset 0 1px 1px rgba(0, 0, 0, 0.075); }
:where(.lab-mm) .form-control::placeholder { color: #999999; }
:where(.lab-mm) .form-control:disabled { background-color: #eeeeee; cursor: not-allowed; }
:where(.lab-mm) textarea.form-control { height: auto; resize: vertical; }
:where(.lab-mm) .help-text { display: block; margin: 10px 0 0; color: rgba(var(--center-channel-color-rgb), 0.75); }
:where(.lab-mm) .help-text ul, :where(.lab-mm) .help-text ol { padding-left: 23px; }
:where(.lab-mm) .help-text a { opacity: 0.5; }

:where(.lab-mm) .lab-mm-radio { display: inline-flex; width: fit-content; align-items: center; gap: 8px; margin: 8px 24px 0 0;
    font-size: 14px; font-weight: 400; line-height: 20px; cursor: pointer; }
:where(.lab-mm) .lab-mm-radio input { display: grid; width: 16px; height: 16px; margin: 0; border: 1px solid rgba(var(--center-channel-color-rgb), 0.24);
    border-radius: 50%; appearance: none; background: #ffffff; place-content: center; }
:where(.lab-mm) .lab-mm-radio input:checked { border-color: var(--button-bg); }
:where(.lab-mm) .lab-mm-radio input::before { width: 8px; height: 8px; border-radius: 50%; background: var(--button-bg); content: ""; transform: scale(0); }
:where(.lab-mm) .lab-mm-radio input:checked::before { transform: scale(1); }

:where(.lab-mm) .config-section { border-radius: 4px; margin-top: 20px; background: #ffffff; box-shadow: 0 2px 3px rgba(0, 0, 0, 0.08); }
:where(.lab-mm) .config-section .admin-console__wrapper { padding: 0; height: auto; }
:where(.lab-mm) .config-section .section-header { padding: 24px 32px; border-bottom: 1px solid rgba(61, 60, 64, 0.08); }
:where(.lab-mm) .config-section .section-header .section-title { font-size: 16px; font-weight: bold; }
:where(.lab-mm) .config-section .section-header .section-subtitle { font-size: 14px; }
:where(.lab-mm) .config-section .section-body { padding: 28px 32px; }
:where(.lab-mm) .config-section .section-body::after { display: table; clear: both; content: " "; }

:where(.lab-mm) .lab-mm-metaline { margin-bottom: 20px; }
:where(.lab-mm) .lab-mm-badge { padding: 3px 10px; border-radius: 40px; background: rgba(61, 184, 135, 0.12); color: rgb(61, 184, 135);
    font-family: 'Open Sans', 'Noto Sans', sans-serif; font-size: 12px; font-weight: 600; letter-spacing: normal; line-height: 16px; }
:where(.lab-mm) .lab-mm-metacard { display: flex; flex-direction: column; gap: 16px; padding: 24px; margin-bottom: 20px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16); border-radius: 4px; background: #ffffff; box-shadow: 0 2px 3px rgba(0, 0, 0, 0.08); }
:where(.lab-mm) .lab-mm-metacard-row { display: flex; align-items: center; justify-content: space-between; gap: 32px; }
:where(.lab-mm) .lab-mm-metacard-id { display: flex; flex-direction: column; gap: 6px; }
:where(.lab-mm) .lab-mm-metacard-name { font-size: 14px; font-weight: 600; line-height: 20px; }
:where(.lab-mm) .lab-mm-metacard-meta { color: rgba(var(--center-channel-color-rgb), 0.75); font-size: 12px; line-height: 16px; }
:where(.lab-mm) .lab-mm-metacard-desc { margin: 0; font-size: 14px; line-height: 20px; }
:where(.lab-mm) .lab-mm-toggle { display: flex; align-items: center; gap: 12px; margin: 0; font-weight: 400; }
:where(.lab-mm) .lab-mm-toggle-track { position: relative; width: 40px; height: 24px; border-radius: 12px; background: var(--button-bg); }
:where(.lab-mm) .lab-mm-toggle-track::after { position: absolute; top: 3px; right: 3px; width: 18px; height: 18px; border-radius: 50%; background: #ffffff; content: ""; }

/* The dark chip: Onyx's variables (mattermost-redux preferences.ts) and the
   page's own white and grey grounds taken down with them. */
.lab-mm[data-dark="true"] {
    --center-channel-bg: #191b1f;
    --center-channel-bg-rgb: 25, 27, 31;
    --center-channel-color: #e3e4e8;
    --center-channel-color-rgb: 227, 228, 232;
    --button-bg: #4a7ce8;
    --button-bg-rgb: 74, 124, 232;
    --link-color: #5d89ea;
    --link-color-rgb: 93, 137, 234;
    --error-text: #da6c6e;
    --error-text-color-rgb: 218, 108, 110;
    --online-indicator: #3db887;
    --away-indicator: #f5ab00;
    --away-indicator-rgb: 245, 171, 0;
    background: #111215;
    color: #e3e4e8;
    color-scheme: dark;
}
:where(.lab-mm[data-dark="true"]) .admin-console .admin-console__header,
:where(.lab-mm[data-dark="true"]) .admin-console .admin-console-save,
:where(.lab-mm[data-dark="true"]) .config-section,
:where(.lab-mm[data-dark="true"]) .lab-mm-metacard,
:where(.lab-mm[data-dark="true"]) .admin-console .banner { background: #191b1f; border-color: rgba(227, 228, 232, 0.12); }
:where(.lab-mm[data-dark="true"]) .config-section .section-header { border-color: rgba(227, 228, 232, 0.08); }
:where(.lab-mm[data-dark="true"]) .admin-console .form-control { border-color: rgba(227, 228, 232, 0.16); background-color: #111215; color: #e3e4e8; }
:where(.lab-mm[data-dark="true"]) .lab-mm-radio input { background: #111215; }
`;
