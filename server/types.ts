export interface StoredAccountRow {
  id: number;
  owner_key: string;
  email_encrypted: string;
  email_hash: string;
  password_encrypted: string;
  client_id_encrypted: string;
  refresh_token_encrypted: string;
  remark: string;
  created_at: string;
  updated_at: string;
  last_sync_at: string | null;
}

export interface AccountCredentials {
  id: number;
  ownerKey: string;
  email: string;
  password: string;
  clientId: string;
  refreshToken: string;
  remark: string;
}

export interface UserRow {
  id: number;
  username: string;
  email_encrypted: string | null;
  email_hash: string | null;
  password_hash: string;
  is_admin: number;
  created_at: string;
}

export interface ImportedAccount {
  email: string;
  password: string;
  clientId: string;
  refreshToken: string;
  remark?: string;
}

export interface PublicAccount {
  id: number;
  email: string;
  remark: string;
  createdAt: string;
  updatedAt: string;
  lastSyncAt: string | null;
}
