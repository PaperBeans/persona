"""Run via browser-use stdin against scripts/mock/skill_mentions.py and the real Vite app.

Set PERSONA_SKILL_SHOTS to a local evidence directory. Screenshots use synthetic data only.
The browser-use CLI preloads the interaction helpers used below.
"""
import os
import time
import urllib.request
from pathlib import Path

shots = Path(os.environ["PERSONA_SKILL_SHOTS"])
shots.mkdir(parents=True, exist_ok=True)
control = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def state(value):
    control.open(f"http://127.0.0.1:25357/mock/skills?state={value}").read()


def shot(name):
    time.sleep(.3)  # let the existing sliding highlight settle
    capture_screenshot(str(shots / f"{name}.png"))


def type_draft(text):
    if js("document.querySelector('[data-composer-input]').tagName === 'TEXTAREA'"):
        fill_input("[data-composer-input]", text)
        return
    js("""(() => {
      const field = document.querySelector('[data-composer-input]');
      field.focus();
      getSelection().selectAllChildren(field);
    })()""")
    cdp("Input.insertText", text=text)


new_tab("http://127.0.0.1:25358/persona/new")
wait_for_load()
js("localStorage.setItem('persona.token','mock'); localStorage.setItem('persona.language','en'); localStorage.removeItem('draft:persona-new'); location.reload()")
wait_for_element("[data-composer-input]", timeout=15)
print(page_info())
print(js("Array.from(document.querySelectorAll('textarea')).map(e=>({placeholder:e.placeholder,disabled:e.disabled}))"))
cdp("Emulation.setDeviceMetricsOverride", width=1365, height=900, deviceScaleFactor=1, mobile=False)
js("document.documentElement.classList.remove('dark')")
type_draft("@review")
wait_for_element("[role=option]", timeout=15)
rows = js("Array.from(document.querySelectorAll('[role=option]')).map(e=>({text:e.textContent,selected:e.getAttribute('aria-selected')}))")
print("Rendered duplicate sources:", rows)
assert len(rows) == 2
press_key("ArrowDown")
shot("light-menu")
js("document.documentElement.classList.add('dark')")
shot("dark-menu")
cdp("Emulation.setDeviceMetricsOverride", width=390, height=844, deviceScaleFactor=1, mobile=True)
cdp("Emulation.setTouchEmulationEnabled", enabled=True)
js("document.documentElement.setAttribute('data-text-size','large')")
shot("narrow-dark-menu")
js("document.documentElement.classList.remove('dark')")
shot("narrow-light-menu")
js("document.documentElement.classList.add('dark')")
assert js("document.documentElement.scrollWidth <= innerWidth")
# Select the highlighted source and remove the token in a single edit.
press_key("Enter")
wait_for_element("[data-inline-mention]", timeout=10)
cdp("Input.insertText", text="Check the example")
time.sleep(.4)
assert js("localStorage.getItem('draft:persona-new').includes('fixture-user')")
shot("narrow-selected")
js("document.documentElement.classList.remove('dark')")
shot("narrow-light-selected")
js("document.documentElement.classList.add('dark')")
assert js("document.querySelector('[data-inline-mention]').title") == "user · ~/example/skills"
js("""(() => {
  const token = document.querySelector('[data-inline-mention]');
  token.parentElement.focus();
  const range = document.createRange();
  range.setStartAfter(token);
  range.collapse(true);
  const selection = getSelection();
  selection.removeAllRanges();
  selection.addRange(range);
})()""")
press_key("Backspace")
assert not js("Boolean(document.querySelector('[data-inline-mention]'))")
# Use a fresh picker so the warm catalog does not stand in for the loading state.
js("location.reload()")
wait_for_load()
wait_for_element("[data-composer-input]", timeout=15)
js("document.documentElement.classList.add('dark')")
state("loading")
type_draft("@security")
wait_for_element("[data-composer-panel] [role=status]", timeout=15)
print("Loading:", js("Array.from(document.querySelectorAll('[role=status]')).map(e=>e.textContent)"))
shot("narrow-loading")
press_key("Escape")
type_draft("Ordinary chat")
state("error")
type_draft("@review")
wait_for_element("[data-composer-panel] [role=status]", timeout=15)
print("Failure:", js("Array.from(document.querySelectorAll('[role=status]')).map(e=>e.textContent)"))
shot("narrow-error")
assert js("document.body.innerText.includes('Skills unavailable here')")
press_key("Escape")
type_draft("Plain chat remains usable")
assert js("!document.querySelector('[data-composer-input]').disabled")
state("partial")
type_draft("@security")
wait_for_element("[data-composer-panel] [role=status]", timeout=15)
js("document.documentElement.classList.remove('dark')")
shot("narrow-light-partial")
print("Partial:", js("Array.from(document.querySelectorAll('[role=status]')).map(e=>e.textContent)"))
assert js("document.body.innerText.includes('Verified local skills only')")
print("PASS rendered keyboard selection, persisted identity, atomic removal, light/dark, narrow large text, loading/failure/partial and ordinary input")
