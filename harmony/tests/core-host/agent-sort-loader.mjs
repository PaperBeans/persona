import { readFileSync } from 'node:fs';
import { registerHooks, stripTypeScriptTypes } from 'node:module';

// Registered before host.mjs so its hooks delegate here rather than replacing this loader.
registerHooks({
  resolve(specifier, context, next) {
    if (context.parentURL?.includes('/entry/src/main/ets/')) {
      const url = new URL(specifier, context.parentURL);
      if (url.pathname.endsWith('/Wire')) url.pathname += '.ets';
      else if (!/\.[a-z]+$/i.test(url.pathname)) url.pathname += '.ts';
      return {url: url.href, shortCircuit: true};
    }
    return next(specifier, context);
  },
  load(url, context, next) {
    if (url.endsWith('.ts') || url.endsWith('.ets')) return {format: 'module', shortCircuit: true,
      source: stripTypeScriptTypes(readFileSync(new URL(url), 'utf8').replace(/@ObservedV2\s*/g, '').replace(/@Trace\s+/g, ''))};
    return next(url, context);
  },
});
