/** Static provider definitions used by onboarding and settings forms. */

export interface ProviderDef {
  id: string
  label: string
  description: string
  fields: ProviderField[]
  models: string[]
}

export interface ProviderField {
  name: string
  label: string
  type: "text" | "password"
  required: boolean
  placeholder?: string
  defaultValue?: string
}

export const providerDefs: ProviderDef[] = [
  {
    id: "anthropic",
    label: "Anthropic",
    description: "Claude models",
    fields: [
      {
        name: "api_key",
        label: "API Key",
        type: "password",
        required: true,
        placeholder: "sk-ant-...",
      },
      {
        name: "base_url",
        label: "Base URL",
        type: "text",
        required: false,
        defaultValue: "https://api.anthropic.com",
      },
    ],
    models: ["claude-sonnet-4-20250514", "claude-haiku-4-5-20251001", "claude-opus-4-5-20250918"],
  },
  {
    id: "openai",
    label: "OpenAI",
    description: "GPT models",
    fields: [
      {
        name: "api_key",
        label: "API Key",
        type: "password",
        required: true,
        placeholder: "sk-...",
      },
      {
        name: "base_url",
        label: "Base URL",
        type: "text",
        required: false,
        defaultValue: "https://api.openai.com/v1",
      },
      {
        name: "organization_id",
        label: "Organization ID",
        type: "text",
        required: false,
      },
    ],
    models: ["gpt-4o", "gpt-4o-mini", "o1-preview", "o3-mini"],
  },
  {
    id: "google",
    label: "Google",
    description: "Gemini models",
    fields: [
      {
        name: "api_key",
        label: "API Key",
        type: "password",
        required: true,
      },
    ],
    models: ["gemini-2.0-flash", "gemini-2.5-pro"],
  },
  {
    id: "bedrock",
    label: "AWS Bedrock",
    description: "Claude via AWS",
    fields: [
      {
        name: "api_key",
        label: "Access Key",
        type: "password",
        required: true,
      },
      {
        name: "project_id",
        label: "Secret Key",
        type: "password",
        required: true,
      },
      {
        name: "base_url",
        label: "Region",
        type: "text",
        required: true,
        defaultValue: "us-east-1",
      },
    ],
    models: ["claude-sonnet-4-20250514"],
  },
  {
    id: "azure",
    label: "Azure OpenAI",
    description: "OpenAI via Azure",
    fields: [
      {
        name: "api_key",
        label: "API Key",
        type: "password",
        required: true,
      },
      {
        name: "base_url",
        label: "Endpoint",
        type: "text",
        required: true,
        placeholder: "https://your-resource.openai.azure.com",
      },
      {
        name: "organization_id",
        label: "Deployment Name",
        type: "text",
        required: true,
      },
    ],
    models: [],
  },
  {
    id: "ollama",
    label: "Ollama",
    description: "Local models",
    fields: [
      {
        name: "base_url",
        label: "Base URL",
        type: "text",
        required: false,
        defaultValue: "http://localhost:11434",
      },
    ],
    models: [],
  },
  {
    id: "custom",
    label: "Custom",
    description: "OpenAI-compatible",
    fields: [
      {
        name: "api_key",
        label: "API Key",
        type: "password",
        required: false,
      },
      {
        name: "base_url",
        label: "Base URL",
        type: "text",
        required: true,
        placeholder: "https://your-api.example.com/v1",
      },
    ],
    models: [],
  },
]
