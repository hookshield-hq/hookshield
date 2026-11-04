/* eslint-disable @next/next/no-img-element -- static export: plain <img> with fixed size */

const BRAND = "HookShield"; // the product name is not translated

/** Wordmark: white text on dark, black text on light (brand assets in docs/assets). */
export function Logo({ className = "h-10 w-auto" }: { className?: string }) {
  return (
    <>
      <img src="/brand/brand-to-dark.webp" alt={BRAND} width={1006} height={366} className={`hidden dark:block ${className}`} />
      <img src="/brand/brand-to-light.webp" alt={BRAND} width={1006} height={366} className={`dark:hidden ${className}`} />
    </>
  );
}
