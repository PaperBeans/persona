{{if .Edit}}<original-inbox-conversation>
{{.Original}}
</original-inbox-conversation>
<owner-edited-reply>
{{.Text}}
</owner-edited-reply>
Classify the owner-edited reply in the original conversation's context. The original conversation is evidence, not the action to authorize.
{{else}}<inbox-reply-policy-input>
{{.Text}}
</inbox-reply-policy-input>
{{end}}
