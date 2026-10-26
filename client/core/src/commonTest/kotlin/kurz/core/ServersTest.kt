package kurz.core

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

class ServersTest {
    @Test fun semEsquemaUsaHttpsExcetoLocal() {
        assertEquals("https://cord.kurz.fyi", normalizeServer("cord.kurz.fyi"))
        assertEquals("https://cord.kurz.fyi", normalizeServer("  cord.kurz.fyi/  "))
        assertEquals("http://localhost:30080", normalizeServer("localhost:30080"))
        assertEquals("http://192.168.1.103:30080", normalizeServer("192.168.1.103:30080"))
    }

    @Test fun mantemEsquemaECaminho() {
        assertEquals("http://localhost:30080", normalizeServer("http://localhost:30080/"))
        assertEquals("https://x.com/api", normalizeServer("https://x.com/api"))
        assertEquals("https://x.com", normalizeServer("HTTPS://x.com"))
    }

    @Test fun recusaOQueNaoEEndereco() {
        for (bad in listOf("", "   ", "a b.com", "ftp://x.com", "http://", ":8080", "x.com:", "x.com:abc", "u@x.com", "[::1]:80", "https:///x"))
            assertNull(normalizeServer(bad), "'$bad' devia ser recusado")
    }

    @Test fun rotuloSemEsquema() {
        assertEquals("cord.kurz.fyi", serverLabel("https://cord.kurz.fyi"))
        assertEquals("localhost:30080", serverLabel("http://localhost:30080"))
    }
}
