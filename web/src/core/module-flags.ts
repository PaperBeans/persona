// The hub flag names of the module host (core/module-host.ts, "The hub flag rule"), in a file of their own so a module's
// descriptor can name its flag without importing the host: src/modules/standalone.ts, which main.tsx loads before App,
// reaches descriptors but must not reach the host (it says why). The host re-exports both.

/** The hello feature a hub that publishes module flags announces (the hub flag rule). */
export const MODULES_FEATURE = "modules";
/** The hello feature that says the hub serves module `id`: what its descriptor names as `feature`. */
export const moduleFeature = (id: string) => `module.${id}`;
