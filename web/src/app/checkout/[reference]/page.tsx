"use client";

// Halaman simulasi pembayaran (mock payment gateway).
// Di produksi halaman ini digantikan halaman payment gateway sungguhan
// yang memanggil webhook /confirm dengan verifikasi signature.

import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { useConfirmPayment } from "@/lib/queries";
import { apiErrorMessage } from "@/lib/api";

function formatIdr(n: number) {
  return new Intl.NumberFormat("id-ID", {
    style: "currency",
    currency: "IDR",
    maximumFractionDigits: 0,
  }).format(n);
}

export default function CheckoutPage() {
  const { reference } = useParams<{ reference: string }>();
  const router = useRouter();
  const confirm = useConfirmPayment();

  const onPay = async () => {
    if (confirm.isPending || confirm.data) return;

    try {
      await confirm.mutateAsync(reference);
      setTimeout(() => router.push("/pricing"), 1200);
    } catch {
      // Error ditampilkan dari state mutation di bawah.
    }
  };

  const errorMessage = confirm.error
    ? apiErrorMessage(confirm.error, "Pembayaran gagal. Silakan coba lagi.")
    : null;

  return (
    <main className="mx-auto flex min-h-[70vh] max-w-md items-center p-8">
      <Card className="w-full">
        <CardHeader>
          <CardTitle>Simulasi Pembayaran</CardTitle>
          <CardDescription>
            Payment gateway mock — tidak ada uang sungguhan.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="flex justify-between text-sm">
            <span className="text-muted-foreground">Referensi</span>
            <span className="font-mono">{reference}</span>
          </div>
          {confirm.data ? (
            <p className="text-center text-sm font-medium text-green-600">
              ✅ Pembayaran berhasil! Langganan Anda aktif. Mengalihkan…
            </p>
          ) : null}
          {errorMessage ? (
            <p role="alert" className="text-center text-sm font-medium text-destructive">
              {errorMessage}
            </p>
          ) : null}
        </CardContent>
        <CardFooter className="flex gap-2">
          <Button
            className="flex-1"
            onClick={onPay}
            disabled={confirm.isPending || !!confirm.data}
          >
            {confirm.isPending
              ? "Memproses…"
              : confirm.data
                ? "Lunas"
                : "Bayar Sekarang (Simulasi)"}
          </Button>
          <Button asChild variant="ghost">
            <Link href="/pricing">Batal</Link>
          </Button>
        </CardFooter>
      </Card>
    </main>
  );
}
