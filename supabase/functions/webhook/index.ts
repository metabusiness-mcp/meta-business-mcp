import { serve } from "https://deno.land/std@0.168.0/http/server.ts"
import { createClient } from 'https://esm.sh/@supabase/supabase-js@2'
import { corsHeaders } from '../_shared/cors.ts'

const supabase = createClient(
  Deno.env.get('SUPABASE_URL')!,
  Deno.env.get('SUPABASE_SERVICE_ROLE_KEY')!
)

const fileExtRegex = /filename=File.([A-z0-9]+)$/

function getFileExtensionFromContentDisposition(contentDisposition: string) {
  const regexResult = fileExtRegex.exec(contentDisposition)
  if (regexResult) {
    return regexResult[1]
  }
  console.log(`${contentDisposition} could not be parsed`)
  return null
}

async function downloadMedia(imageMessage: any, supabaseClient: any) {
  const mediaDetails = imageMessage.image || imageMessage.video || imageMessage.document || imageMessage.audio || imageMessage.sticker
  if (!mediaDetails) {
    throw new Error("media details not available in media keys")
  }
  const headerOptions = {
    'Authorization': `Bearer ${Deno.env.get('WHATSAPP_ACCESS_TOKEN')}`,
    'User-Agent': 'curl/7.84.0',
    'Accept': '*/*',
  }
  const firstResponse = await fetch(`https://graph.facebook.com/v15.0/${mediaDetails.id}`, { headers: headerOptions })

  if (!firstResponse.ok) {
    const responseText = await firstResponse.text()
    throw new Error(`First response error - status: ${firstResponse.status}, response: ${responseText}`)
  }

  const firstResponseBody = await firstResponse.json()

  const mediaResponse = await fetch(firstResponseBody.url, { headers: headerOptions })

  if (!mediaResponse.ok) {
    throw new Error(`Media response error - status: ${mediaResponse.status}`)
  }

  const arrayBuffer = await mediaResponse.arrayBuffer()
  if (arrayBuffer) {
    const contentDisposition = mediaResponse.headers.get("content-disposition")
    let extension
    if (contentDisposition) {
      extension = getFileExtensionFromContentDisposition(contentDisposition)
    }
    if (!extension) {
      extension = 'unknown'
    }

    const { data, error } = await supabaseClient
      .storage
      .from('media')
      .upload(`${imageMessage.from}/${mediaDetails.id}.${extension}`, arrayBuffer, {
        cacheControl: '3600',
        contentType: mediaResponse.headers.get('content-type') || undefined,
        upsert: false,
      })
    if (error) throw error
    console.log(`media stored at ${data.path}`)
    const updateResult = await supabaseClient
      .from('messages')
      .update({ media_url: data.path })
      .eq('wam_id', imageMessage.id)
    if (updateResult.error) throw updateResult.error
  } else {
    console.warn('mediaBody is null')
  }
}

async function updateBroadCastStatus(supabaseClient: any, status: any) {
  const update_obj: {
    sent_at?: Date,
    delivered_at?: Date,
    read_at?: Date,
    failed_at?: Date,
  } = {}
  if (status.status === 'sent') {
    update_obj.sent_at = new Date(Number.parseInt(status.timestamp) * 1000)
  } else if (status.status === 'delivered') {
    update_obj.delivered_at = new Date(Number.parseInt(status.timestamp) * 1000)
  } else if (status.status === 'read') {
    update_obj.read_at = new Date(Number.parseInt(status.timestamp) * 1000)
  } else if (status.status === 'failed') {
    update_obj.failed_at = new Date(Number.parseInt(status.timestamp) * 1000)
  } else {
    console.warn(`Unknown status : ${status.status}`)
    console.warn('status', status)
    return
  }
  const { data: broadcastContactData, error: broadcastContactUpdateError } = await supabaseClient
    .from('broadcast_contact')
    .update(update_obj)
    .eq('wam_id', status.id)
    .select()
  if (broadcastContactData && broadcastContactData.length > 0) {
    const singleContact = broadcastContactData[0]
    if (broadcastContactUpdateError) throw new Error('Error while updating broadcast contact', { cause: broadcastContactUpdateError })

    if (status.status === 'read') {
      const argsToUpdateCount = {
        read_count_to_be_added: 1,
        b_id: singleContact.broadcast_id
      }
      const { error: countUpdateError } = await supabaseClient.rpc('add_read_count_to_broadcast', argsToUpdateCount)
      if (countUpdateError) {
        console.error(`Error while updating read count for status.id: ${status.id}`, countUpdateError)
      }
    } else if (status.status === 'delivered') {
      const argsToUpdateCount = {
        delivered_count_to_be_added: 1,
        b_id: singleContact.broadcast_id
      }
      const { error: countUpdateError } = await supabaseClient.rpc('add_delivered_count_to_broadcast', argsToUpdateCount)
      if (countUpdateError) {
        console.error(`Error while updating delivered count for status.id: ${status.id}`, countUpdateError)
      }
    } else if (status.status === 'sent') {
      const argsToUpdateCount = {
        sent_count_to_be_added: 1,
        b_id: singleContact.broadcast_id
      }
      const { error: countUpdateError } = await supabaseClient.rpc('add_sent_count_to_broadcast', argsToUpdateCount)
      if (countUpdateError) {
        console.error(`Error while updating sent count for status.id: ${status.id}`, countUpdateError)
      }
    } else if (status.status === 'failed') {
      const argsToUpdateCount = {
        failed_count_to_be_added: 1,
        b_id: singleContact.broadcast_id
      }
      const { error: countUpdateError } = await supabaseClient.rpc('add_failed_count_to_broadcast', argsToUpdateCount)
      if (countUpdateError) {
        console.error(`Error while updating failed count for status.id: ${status.id}`, countUpdateError)
      }
    }
  }
}

async function updateBroadCastReplyStatus(supabaseClient: any, messages: any[]) {
  if (messages.length > 0) {
    const message = messages[0]
    const { data: broadcastContactData, error: broadcastGetError } = await supabaseClient
      .from('broadcast_contact')
      .select('*')
      .eq('contact_id', message.from)
      .order('created_at', { ascending: false })
      .limit(1)
    if (broadcastGetError) throw new Error('Error while getting broadcast contact', { cause: broadcastGetError })
    if (broadcastContactData && broadcastContactData.length > 0) {
      const singleContact = broadcastContactData[0]
      if (broadcastContactData && !singleContact.reply_counted) {
        const { error: countUpdateError } = await supabaseClient.rpc('add_replied_to_broadcast_contact', {
          b_id: singleContact.broadcast_id,
          replied_count_to_be_added: 1
        })
        if (countUpdateError) {
          console.error(`Error while updating count for singleContact.broadcast_id: ${singleContact.broadcast_id}, message.id: ${message.id}`, countUpdateError)
        } else {
          const { error: broadcastContactUpdateError } = await supabaseClient
            .from('broadcast_contact')
            .update({ reply_counted: true })
            .eq('id', singleContact.id)
          if (broadcastContactUpdateError) {
            console.error(`Error while updating singleContact.id: ${singleContact.id}, message.id: ${message.id}`, broadcastContactUpdateError)
          }
        }
      }
    }
  }
}

async function verifySignature(message: string, signature: string, secret: string): Promise<boolean> {
  const prefix = 'sha256='
  if (!signature.startsWith(prefix)) {
    return false
  }
  const sigWithoutPrefix = signature.slice(prefix.length)

  const encoder = new TextEncoder()
  const key = await crypto.subtle.importKey(
    "raw",
    encoder.encode(secret),
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign"]
  )

  const signatureBuffer = await crypto.subtle.sign(
    "HMAC",
    key,
    encoder.encode(message)
  )

  const hashArray = Array.from(new Uint8Array(signatureBuffer))
  const messageHash = hashArray.map(b => b.toString(16).padStart(2, '0')).join('')

  return sigWithoutPrefix === messageHash
}

serve(async (req) => {
  if (req.method === 'OPTIONS') {
    return new Response('ok', { headers: corsHeaders })
  }

  // Handle GET: WhatsApp Webhook Verification
  if (req.method === 'GET') {
    const url = new URL(req.url)
    const mode = url.searchParams.get('hub.mode')
    const token = url.searchParams.get('hub.verify_token')
    const challenge = url.searchParams.get('hub.challenge')

    if (mode && token && challenge && mode === 'subscribe') {
      const verifyToken = Deno.env.get('WEBHOOK_VERIFY_TOKEN')
      const isValid = token === verifyToken
      if (isValid) {
        return new Response(challenge, { status: 200, headers: corsHeaders })
      } else {
        return new Response('Forbidden', { status: 403, headers: corsHeaders })
      }
    } else {
      return new Response('Bad Request', { status: 400, headers: corsHeaders })
    }
  }

  // Handle POST: WhatsApp Event Updates
  if (req.method === 'POST') {
    try {
      const signature = req.headers.get('x-hub-signature-256')
      const rawRequestBody = await req.text()
      const appSecret = Deno.env.get('FACEBOOK_APP_SECRET')

      if (!signature || !appSecret || !(await verifySignature(rawRequestBody, signature, appSecret))) {
        console.warn(`Invalid signature: ${signature}`)
        return new Response('Unauthorized', { status: 401, headers: corsHeaders })
      }

      const webhookBody = JSON.parse(rawRequestBody)
      if (webhookBody.entry && webhookBody.entry.length > 0) {
        const { error: insertError } = await supabase
          .from('webhook')
          .insert(webhookBody.entry.map((entry: any) => {
            return { payload: entry }
          }))
        if (insertError) throw insertError

        const changes = webhookBody.entry[0].changes
        if (changes && changes.length > 0) {
          if (changes[0].field === "messages") {
            const changeValue = changes[0].value
            const contacts = changeValue.contacts
            const messages = changeValue.messages
            const statuses = changeValue.statuses
            const echoes = changeValue.message_echoes

            if (contacts && contacts.length > 0) {
              for (const contact of contacts) {
                const upsertData: any = {
                  wa_id: contact.wa_id,
                  last_message_at: new Date(),
                  last_message_received_at: new Date(),
                  in_chat: true,
                  ...(contact.profile?.name && { profile_name: contact.profile.name }),
                }
                const { error: contactError } = await supabase
                  .from('contacts')
                  .upsert(upsertData)
                if (contactError) throw contactError
              }
            }

            if (messages && messages.length > 0) {
              const { error: messageError } = await supabase
                .from('messages')
                .upsert(messages.map((message: any) => {
                  return {
                    chat_id: message.from,
                    message: message,
                    wam_id: message.id,
                    created_at: new Date(Number.parseInt(message.timestamp) * 1000),
                    is_received: true,
                  }
                }), { onConflict: 'wam_id', ignoreDuplicates: true })
              
              if (messageError) throw new Error("Error while inserting messages to database", { cause: messageError })

              for (const message of messages) {
                if (message.type === 'image' || message.type === 'video' || message.type === 'document' || message.type === 'audio' || message.type === 'sticker') {
                  try {
                    await downloadMedia(message, supabase)
                  } catch (err) {
                    console.error("Failed to download media for message:", message.id, err)
                  }
                }
              }

              try {
                await updateBroadCastReplyStatus(supabase, messages)
              } catch (err) {
                console.error("Failed to update broadcast reply status:", err)
              }

              try {
                const chatIds = messages.map((m: any) => m.from).filter((m: any, i: number, a: any[]) => a.indexOf(m) === i)
                await supabase.functions.invoke('update-unread-count', {
                  body: { chat_id: chatIds }
                })
              } catch (err) {
                console.error("Failed to invoke update-unread-count:", err)
              }
            }

            if (echoes && echoes.length > 0) {
              const { error: echoError } = await supabase
                .from('messages')
                .upsert(echoes.map((message: any) => {
                  const chatId = message.to || message.from
                  return {
                    chat_id: Number.parseInt(chatId),
                    message: {
                      ...message,
                      via_wa_app: true,
                      to: chatId,
                    },
                    wam_id: message.id,
                    created_at: new Date(Number.parseInt(message.timestamp) * 1000),
                    is_received: false,
                    sent_via: 'mobile_app',
                  }
                }), { onConflict: 'wam_id', ignoreDuplicates: true })

              if (echoError) throw new Error("Error while inserting message echoes to database", { cause: echoError })

              for (const message of echoes) {
                if (message.type === 'image' || message.type === 'video' || message.type === 'document' || message.type === 'audio' || message.type === 'sticker') {
                  try {
                    await downloadMedia(message, supabase)
                  } catch (err) {
                    console.error("Failed to download media for echo:", message.id, err)
                  }
                }
              }

              for (const message of echoes) {
                const chatId = message.to || message.from
                await supabase
                  .from('contacts')
                  .update({ last_message_at: new Date() })
                  .eq('wa_id', Number.parseInt(chatId))
              }
            }

            if (statuses && statuses.length > 0) {
              for (const status of statuses) {
                const update_obj: any = {
                  wam_id_in: status.id,
                }
                let functionName: string | null = null
                const statusDate = new Date(Number.parseInt(status.timestamp) * 1000)

                if (status.status === 'sent') {
                  update_obj.sent_at_in = statusDate
                  functionName = 'update_message_sent_status'
                } else if (status.status === 'delivered') {
                  update_obj.delivered_at_in = statusDate
                  functionName = 'update_message_delivered_status'
                } else if (status.status === 'read') {
                  update_obj.read_at_in = statusDate
                  functionName = 'update_message_read_status'
                } else if (status.status === 'failed') {
                  update_obj.failed_at_in = statusDate
                  functionName = 'update_message_failed_status'
                } else {
                  console.warn(`Unknown status : ${status.status}`)
                  console.warn('status', status)
                  continue
                }

                if (functionName) {
                  const { data, error: updateStatusError } = await supabase.rpc(functionName, update_obj)
                  if (updateStatusError) {
                    throw new Error(`Error while updating status, functionName: ${functionName} wam_id: ${status.id} status: ${status.status}`, { cause: updateStatusError })
                  }
                  console.log(`${functionName} data`, data)
                  if (data) {
                    try {
                      await updateBroadCastStatus(supabase, status)
                    } catch (err) {
                      console.error("Failed to update broadcast status:", err)
                    }
                  } else {
                    console.warn(`Status already updated : ${status.id} : ${status.status}`)
                  }
                }
              }
            }
          } else if (changes[0].field === "smb_message_echoes") {
            const changeValue = changes[0].value
            const echoes = changeValue.message_echoes || changeValue.smb_message_echoes

            if (echoes && echoes.length > 0) {
              const { error: echoError } = await supabase
                .from('messages')
                .upsert(echoes.map((message: any) => {
                  const chatId = message.to || message.from
                  return {
                    chat_id: Number.parseInt(chatId),
                    message: {
                      ...message,
                      via_wa_app: true,
                      to: chatId,
                    },
                    wam_id: message.id,
                    created_at: new Date(Number.parseInt(message.timestamp) * 1000),
                    is_received: false,
                    sent_via: 'mobile_app',
                  }
                }), { onConflict: 'wam_id', ignoreDuplicates: true })

              if (echoError) throw new Error("Error while inserting smb message echoes to database", { cause: echoError })

              for (const message of echoes) {
                if (message.type === 'image' || message.type === 'video' || message.type === 'document' || message.type === 'audio' || message.type === 'sticker') {
                  try {
                    await downloadMedia(message, supabase)
                  } catch (err) {
                    console.error("Failed to download media for echo:", message.id, err)
                  }
                }
              }

              for (const message of echoes) {
                const chatId = message.to || message.from
                await supabase
                  .from('contacts')
                  .update({ last_message_at: new Date() })
                  .eq('wa_id', Number.parseInt(chatId))
              }
            }
          }
        }
      }

      // Self-ping send-message to keep it warm (fire and forget)
      const supabaseUrl = Deno.env.get('SUPABASE_URL')
      if (supabaseUrl) {
        fetch(`${supabaseUrl}/functions/v1/send-message`, {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
          },
          body: JSON.stringify({}),
        }).catch((err) => console.error("Warmup self-ping failed:", err))
      }

      const MCP_SERVER_URL = Deno.env.get("MCP_WEBHOOK_URL");
      if (MCP_SERVER_URL) {
        const rawBody = rawRequestBody;
        EdgeRuntime.waitUntil(
          fetch(`${MCP_SERVER_URL}/webhook`, {
            method: "POST",
            headers: {
              "Content-Type": "application/json",
              "x-hub-signature-256":
                req.headers.get("x-hub-signature-256") ?? "",
            },
            body: rawBody,
          }).catch(() => {
            console.log("MCP forward failed, ignoring");
          })
        );
      }

      return new Response(JSON.stringify({ success: true }), {
        status: 200,
        headers: { ...corsHeaders, 'Content-Type': 'application/json' },
      })
    } catch (err) {
      console.error("Error processing POST request:", err)
      return new Response(JSON.stringify({ error: err.message }), {
        status: 500,
        headers: { ...corsHeaders, 'Content-Type': 'application/json' },
      })
    }
  }

  return new Response('Method Not Allowed', { status: 405, headers: corsHeaders })
})
