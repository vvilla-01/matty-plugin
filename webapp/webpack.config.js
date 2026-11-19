const path = require('path');

module.exports = {
    entry: './src/index.tsx',
    resolve: {
        extensions: ['.ts', '.tsx', '.js', '.jsx'],
        modules: ['src', 'node_modules'],
    },
    module: {
        rules: [
            {
                // The stylesheet's comments and indentation come out before
                // ts-loader sees it, and nothing else does. The lab's config
                // has the same rule, so the lab runs the CSS that ships.
                // build/check-squeeze.mjs proves the CSS is unchanged.
                test: /styles\.ts$/,
                exclude: /node_modules/,
                enforce: 'pre',
                use: path.resolve(__dirname, '..', 'build', 'squeeze-css-loader.js'),
            },
            {
                test: /\.tsx?$/,
                exclude: /node_modules/,
                use: {
                    loader: 'ts-loader',
                    options: {transpileOnly: true},
                },
            },
        ],
    },

    // Mattermost provides these on the page; bundling copies would break hooks.
    externals: {
        react: 'React',
        'react-dom': 'ReactDOM',
        redux: 'Redux',
        'react-redux': 'ReactRedux',
        'prop-types': 'PropTypes',
        'react-bootstrap': 'ReactBootstrap',
    },
    output: {
        path: path.join(__dirname, 'dist'),
        publicPath: '/',
        filename: 'main.js',
    },
    performance: {hints: false},
    mode: 'production',
};
