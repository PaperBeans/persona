// The module rows of a core Settings page (core/module-host.ts `settingsRows`, docs/web/web.md "Modules"): what enabled
// modules add to a section of a page the core draws, such as the Lark connector's section of Settings › Connectors
// (#13404). A module's code loads when the section first draws; until it is in, and for a module that is off, the place
// draws nothing, so with no module naming it the section is as it was. A module's row that throws is left out.
import { Component, useEffect, useMemo, type ReactNode } from "react";
import { loadModule, settingsRowFillers, useModuleStates, type SettingsRowPlace } from "./module-host";

class RowBoundary extends Component<{ id: string; children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  componentDidCatch(error: unknown) {
    console.error(`module "${this.props.id}" failed in a Settings row`, error);
  }
  render() {
    return this.state.failed ? null : this.props.children;
  }
}

/** The enabled modules' rows for `place`, each once its code is in. */
export function SettingsRows({ place }: { place: SettingsRowPlace }) {
  const states = useModuleStates();
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const fillers = useMemo(() => settingsRowFillers(place), [place, states]);
  const missing = fillers.filter((f) => !f.Component).map((f) => f.id).join(",");
  useEffect(() => {
    for (const id of missing ? missing.split(",") : []) loadModule(id).catch((e: unknown) => console.error(`module "${id}" could not load`, e));
  }, [missing]);
  return (
    <>
      {fillers.map(({ id, Component: Row }) =>
        Row ? (
          <RowBoundary key={id} id={id}>
            <Row />
          </RowBoundary>
        ) : null,
      )}
    </>
  );
}
