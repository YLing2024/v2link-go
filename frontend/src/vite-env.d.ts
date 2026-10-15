/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_PUBLIC_HOST?: string
  readonly VITE_PUBLIC_SNI?: string
  readonly VITE_PUBLIC_PATH?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
