{{if eq .Who "persona"}}Persona app message, not the user{{else if eq .Who "agent"}}Agent session {{ctx .From}}, not the user{{else}}User{{end}}
