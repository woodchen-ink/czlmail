import { getLocale } from "@/lib/i18n";

/** Same choice as the backend GetAIConfig: the AI translation target follows the UI language until one is saved. */
const DEFAULT_TRANSLATE_LANGS: Record<string, string> = { en: "English", cs: "Čeština", ru: "Русский" };

export function defaultTranslateLang(): string {
  return DEFAULT_TRANSLATE_LANGS[getLocale().split("-")[0]] ?? "简体中文";
}
