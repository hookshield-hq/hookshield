import { Footer } from "@/components/footer";
import { Header } from "@/components/header";
import { Landing } from "@/components/landing";
import { landingMetadata } from "@/lib/metadata";

export const metadata = landingMetadata("en");

export default function Home() {
  return (
    <>
      <Header locale="en" current="home" />
      <Landing locale="en" />
      <Footer locale="en" />
    </>
  );
}
