// @vulos/apps-ui — public entry.
//
// import AppsAndBots, { makeClient, PRODUCTS, ALL_SCOPES } from "@vulos/apps-ui";
// import "@vulos/apps-ui/styles.css"; // when consuming the built library
export { default as AppsAndBots, default } from "./AppsAndBots";
export { makeClient, PRODUCTS, ALL_SCOPES } from "./api";

// Component prop types.
export type {
  AppsAndBotsProps,
  ProductModeProps,
  AggregateModeProps,
  AggregateSource,
  NormalizedSource,
  Theme,
} from "./AppsAndBots";
export type { AppCardProps } from "./components/AppCard";
export type { InstallFormProps } from "./components/InstallForm";

// API client types.
export type {
  AppsClient,
  MakeClientOptions,
  Fetcher,
  ApiError,
  ProductMeta,
} from "./api";

// Domain model — the wire contract mirrored from appsplatform (Go).
export type {
  ProductId,
  Scope,
  KnownScope,
  SlashCommand,
  IncomingWebhook,
  AppSummary,
  Manifest,
  UpdateManifest,
  CreateAppResponse,
  RotateTokenResponse,
  RotateSecretResponse,
  RemoveResponse,
} from "./types";
