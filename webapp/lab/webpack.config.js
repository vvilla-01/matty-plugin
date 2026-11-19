const path = require('path');
const webpack = require('webpack');

// The lab bundle.
//
// Same sources as the plugin, with exactly six modules swapped: the HTTP
// client, the WebRTC player, Twitch's player and windows (twitchEmbed, so the
// lab never frames Twitch or opens a window), the manifest (which is only
// swapped so the fonts can be inlined), and the soundboard's emoji pictures
// and sound maker (below). Everything else — the components, the
// stylesheet, the date handling — is the shipping code, which is the entire
// point.
//
// The regexes are anchored on purpose. A looser /client$/ also matches
// 'react-dom/client' and replaces React's own entry point with the mock, which
// fails in a way that looks nothing like its cause.
const swap = (pattern, file) => new webpack.NormalModuleReplacementPlugin(
    pattern,
    (resource) => {
        resource.request = path.resolve(__dirname, file);
    },
);

module.exports = {
    context: __dirname,
    entry: './index.tsx',
    resolve: {
        extensions: ['.ts', '.tsx', '.js', '.jsx'],
    },
    module: {
        rules: [
            {
                // The same squeeze as the plugin's build, so the stylesheet
                // here is the one that ships; see build/squeeze-css-loader.js.
                test: /styles\.ts$/,
                exclude: /node_modules/,
                enforce: 'pre',
                use: path.resolve(__dirname, '..', '..', 'build', 'squeeze-css-loader.js'),
            },
            {
                test: /\.tsx?$/,
                exclude: /node_modules/,
                use: {
                    loader: 'ts-loader',
                    options: {
                        transpileOnly: true,
                        configFile: path.resolve(__dirname, '..', 'tsconfig.json'),
                    },
                },
            },
            {
                test: /\.woff2$/,
                type: 'asset/inline',
            },
            {
                // The emblem behind the panel. Inlined for the same reason as
                // the fonts: the lab is one file with no server behind it.
                test: /\.(svg|png|jpg)$/,
                type: 'asset/inline',
            },
            {
                // The sample clip, about 416 KB, for every clip the lab plays.
                // Inlined too; the mock client turns it into a blob once, so
                // a picture drawn from it leaves the canvas readable.
                test: /\.mp4$/,
                type: 'asset/inline',
            },
        ],
    },
    plugins: [
        swap(/^\.{1,2}\/client$/, 'mocks/client.ts'),
        swap(/^\.{1,2}\/whep$/, 'mocks/whep.ts'),
        swap(/^\.{1,2}\/twitchEmbed$/, 'mocks/twitchEmbed.tsx'),
        swap(/^\.{1,2}\/manifest$/, 'mocks/manifest.ts'),

        // The soundboard (design/3.2.0.md 7): emoji pictures from the few the
        // lab carries, and a stand-in for the microphone, the decoder and the
        // encoder, which the lab has nothing real to feed.
        swap(/^\.{1,2}\/emojiImages$/, 'mocks/emojiImages.ts'),
        swap(/^\.{1,2}\/soundEncode$/, 'mocks/soundEncode.ts'),
    ],

    // React is NOT external here. Mattermost provides it on the page; a single
    // file opened from the filesystem has to carry its own. What it carries is
    // package.json's React 18.2.0, the one Mattermost 11.11 ships, so an API
    // that React lacks fails here exactly as it would there.
    // build/check-react.py keeps the two the same.
    output: {
        path: path.join(__dirname, 'dist'),
        filename: 'lab.js',
    },
    performance: {hints: false},
    mode: 'production',
    devtool: false,
};
