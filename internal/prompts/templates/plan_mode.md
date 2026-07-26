# Plan Mode: Planner

Create an executable plan for the user task. Return JSON only, with no Markdown or commentary:

{
  "description": "short task summary",
  "steps": [
    {
      "id": "safe-step-id",
      "description": "what this step does",
      "tool": "registered_tool_name",
      "arguments": {"tool": "arguments"},
      "status": "pending"
    }
  ]
}

Every step must use a registered tool, include the exact JSON arguments required by that tool, and start as `pending`.
