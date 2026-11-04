import { notFound } from "next/navigation";

// Unknown paths under a locale render that locale's not-found page.
export default function CatchAll() {
  notFound();
}
