import type { Metadata } from "next";
import { Archivo, Spectral } from "next/font/google";
import "./globals.css";

// Spectral: a screen serif from Production Type, a French foundry. Chosen for
// the subject rather than reached for by habit.
const spectral = Spectral({
  subsets: ["latin"],
  weight: ["300", "400", "600"],
  variable: "--font-spectral",
  display: "swap",
});

// Archivo: grotesque with genuine tabular numerals, which the clock axis needs.
const archivo = Archivo({
  subsets: ["latin"],
  weight: ["400", "500", "600"],
  variable: "--font-archivo",
  display: "swap",
});

export const metadata: Metadata = {
  title: "Flâneur — walking itineraries for Paris",
  description:
    "Turn a sentence into a routed walking itinerary across Paris, built from OpenStreetMap and solved locally.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" className={`${spectral.variable} ${archivo.variable}`}>
      <body>{children}</body>
    </html>
  );
}
