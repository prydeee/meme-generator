"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useState } from "react";
import {
  intervalToDuration,
  formatDuration,
  parseISO,
} from "date-fns";
import { id } from "date-fns/locale";
import { Button } from "@/components/ui/button";
import {
  HoverCard,
  HoverCardContent,
  HoverCardTrigger,
} from "@/components/ui/hover-card";
import { Separator } from "@/components/ui/separator";
import { useLogout, useMe } from "@/lib/auth";
import { useSubscriptionStatus } from "@/lib/queries";

const links = [
  { href: "/studio", label: "Studio" },
  { href: "/pricing", label: "Paket" },
];

export function SiteHeader() {
  const pathname = usePathname();
  const me = useMe();
  const logout = useLogout();
  const user = me.data;
  const sub = useSubscriptionStatus(me.data?.id, !me.isPending);

  // Countdown masa aktif: tick tiap detik agar sisa waktu selalu berjalan.
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);

  const sisaWaktu = sub.data?.subscribed && sub.data.endsAt
    ? (() => {
        const ends = parseISO(sub.data.endsAt).getTime();
        if (ends <= now) return "habis";
        const dur = intervalToDuration({ start: now, end: ends });
        const teks = formatDuration(dur, {
          locale: id,
          format: ["days", "hours", "minutes", "seconds"],
        });
        // "Berakhir dalam X" saat hover; tanpa awalan "in " (locale id).
        return teks;
      })()
    : null;

  return (
    <header className="border-b">
      <div className="mx-auto flex h-14 max-w-5xl items-center gap-6 px-4">
        <Link
          href="/"
          className="text-lg font-bold"
          style={{ fontFamily: "var(--font-impact)" }}
        >
          MEME PORTAL
        </Link>

        <nav className="flex items-center gap-4 text-sm">
          {links.map((l) => (
            <Link
              key={l.href}
              href={l.href}
              className={
                pathname.startsWith(l.href)
                  ? "font-medium text-foreground"
                  : "text-muted-foreground hover:text-foreground"
              }
            >
              {l.label}
            </Link>
          ))}
        </nav>

        <div className="ml-auto flex items-center gap-2">
          {user ? (
            <>
              <HoverCard openDelay={100} closeDelay={150}>
                <HoverCardTrigger asChild>
                  <button
                    className="cursor-pointer rounded px-2 py-1 text-sm text-muted-foreground underline-offset-4 hover:underline"
                    aria-label="Info akun"
                  >
                    {user.name || user.email}
                  </button>
                </HoverCardTrigger>
                <HoverCardContent className="w-72" align="end">
                  <div className="space-y-1">
                    <p className="text-sm font-semibold">{user.name}</p>
                    <p className="text-sm text-muted-foreground">
                      {user.email}
                    </p>
                  </div>
                  <Separator className="my-3" />
                  <div className="space-y-1 text-sm">
                    <p className="font-medium">Langganan</p>
                    {sub.data?.subscribed ? (
                      <>
                        <p className="text-muted-foreground">
                          Paket: {sub.data.planName}
                        </p>
                        <p className="text-muted-foreground">
                          Berakhir dalam{" "}
                          <span className="font-medium text-foreground">
                            {sisaWaktu}
                          </span>
                        </p>
                      </>
                    ) : (
                      <p className="text-muted-foreground">
                        Belum berlangganan —{" "}
                        <Link href="/pricing" className="underline">
                          pilih paket
                        </Link>
                      </p>
                    )}
                  </div>
                </HoverCardContent>
              </HoverCard>
              <Button
                variant="outline"
                size="sm"
                onClick={() => logout.mutate()}
                disabled={logout.isPending}
              >
                Keluar
              </Button>
            </>
          ) : (
            <>
              <Button asChild variant="ghost" size="sm">
                <Link href="/login">Masuk</Link>
              </Button>
              <Button asChild size="sm">
                <Link href="/register">Daftar</Link>
              </Button>
            </>
          )}
        </div>
      </div>
    </header>
  );
}
