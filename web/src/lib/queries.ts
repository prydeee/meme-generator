import {
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useEffect } from "react";
import { api, unwrap } from "@/lib/api";
import type {
  CheckoutResponse,
  Meme,
  Plan,
  SubscriptionStatus,
  UploadResponse,
} from "@/lib/types";

export const queryKeys = {
  plans: ["plans"] as const,
  subscription: ["subscription"] as const,
  subscriptionForUser: (userID: string) => ["subscription", userID] as const,
  memes: (limit: number, offset: number) => ["memes", limit, offset] as const,
};

const checkoutIntentStoragePrefix = "meme_portal_checkout_intents:";
const checkoutActiveUserStorageKey = "meme_portal_checkout_active_user";

export function clearCheckoutIntents() {
  if (typeof window === "undefined") return;
  const activeUserID = sessionStorage.getItem(checkoutActiveUserStorageKey);
  if (activeUserID) {
    sessionStorage.removeItem(`${checkoutIntentStoragePrefix}${activeUserID}`);
  }
  sessionStorage.removeItem(checkoutActiveUserStorageKey);
}

export function usePlans() {
  return useQuery({
    queryKey: queryKeys.plans,
    queryFn: async () =>
      unwrap<Plan[]>(await api.get("/api/v1/subscription/plans")) ?? [],
  });
}

export function useSubscriptionStatus(userID?: string, authResolved = true) {
  const queryKey = userID
    ? queryKeys.subscriptionForUser(userID)
    : queryKeys.subscription;

  return useQuery({
    queryKey,
    enabled: authResolved && Boolean(userID),
    staleTime: 0,
    refetchOnMount: "always",
    queryFn: async () =>
      unwrap<SubscriptionStatus>(
        await api.get("/api/v1/subscription/status", {
          params: { _: Date.now() },
        }),
      ),
  });
}

export function useCheckout(userID?: string, authResolved = true) {
  const qc = useQueryClient();
  const activeUserStorageKey = checkoutActiveUserStorageKey;
  const storageKey = userID
    ? `${checkoutIntentStoragePrefix}${userID}`
    : null;
  type CheckoutVariables = { planCode: string; idempotencyKey: string };

  useEffect(() => {
    if (typeof window === "undefined" || !authResolved) return;

    const previousUserID = sessionStorage.getItem(activeUserStorageKey);
    if (previousUserID && previousUserID !== userID) {
      sessionStorage.removeItem(`${checkoutIntentStoragePrefix}${previousUserID}`);
    }
    if (userID) {
      sessionStorage.setItem(activeUserStorageKey, userID);
    } else {
      sessionStorage.removeItem(activeUserStorageKey);
    }
  }, [activeUserStorageKey, authResolved, userID]);

  const readIntents = (): Record<string, string> => {
    if (typeof window === "undefined" || !storageKey) return {};
    try {
      return JSON.parse(sessionStorage.getItem(storageKey) ?? "{}");
    } catch {
      return {};
    }
  };

  const writeIntents = (intents: Record<string, string>) => {
    if (typeof window !== "undefined" && storageKey) {
      sessionStorage.setItem(storageKey, JSON.stringify(intents));
    }
  };

  const beginCheckout = (planCode: string): CheckoutVariables => {
    const intents = readIntents();
    const idempotencyKey = intents[planCode] ?? crypto.randomUUID();
    intents[planCode] = idempotencyKey;
    if (userID) writeIntents(intents);
    return { planCode, idempotencyKey };
  };

  const mutation = useMutation({
    mutationFn: async ({ planCode, idempotencyKey }: CheckoutVariables) =>
      unwrap<CheckoutResponse>(
        await api.post("/api/v1/subscription/checkout", { planCode }, {
          headers: { "Idempotency-Key": idempotencyKey },
        }),
      ),
    onSuccess: (_, { planCode, idempotencyKey }) => {
      const intents = readIntents();
      if (intents[planCode] === idempotencyKey) {
        delete intents[planCode];
        writeIntents(intents);
      }
      return qc.invalidateQueries({ queryKey: queryKeys.subscription });
    },
  });

  return { ...mutation, beginCheckout };
}

export function useConfirmPayment() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (reference: string) =>
      unwrap(
        await api.post(
          `/api/v1/subscription/payments/${reference}/confirm`,
        ),
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.subscription }),
  });
}

export function useMemes(limit = 20, offset = 0) {
  return useQuery({
    queryKey: queryKeys.memes(limit, offset),
    queryFn: async () =>
      unwrap<{ items: Meme[]; total: number }>(
        await api.get("/api/v1/memes", { params: { limit, offset } }),
      ),
  });
}

export function usePrepareUpload() {
  return useMutation({
    mutationFn: async (input: { title: string; filename: string }) =>
      unwrap<UploadResponse>(
        await api.post("/api/v1/memes/upload", input),
      ),
  });
}

export function useCompleteMeme() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: { id: string; resultUrl: string }) =>
      unwrap<Meme>(
        await api.post(`/api/v1/memes/${input.id}/complete`, {
          resultUrl: input.resultUrl,
        }),
      ),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["memes"] });
      return qc.invalidateQueries({ queryKey: queryKeys.subscription });
    },
  });
}

export function useDeleteMeme() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: string) => api.delete(`/api/v1/memes/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["memes"] }),
  });
}
