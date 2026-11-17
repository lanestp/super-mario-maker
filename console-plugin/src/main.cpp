// A plugin for a Wii U running Aroma that sends Super Mario Maker, and
// nothing else, to a private server.
//
// When a game wants its game server, it asks the console's account
// system for a "NEX token" for the game server's ID. The answer says
// where the server is and the password to log in to it with. This
// plugin lets the console's own network, such as Pretendo, answer as
// usual, and then changes the answer for Super Mario Maker's game
// server only. Everything else, and every other game, stays where it
// was.
//
// The server and the password are built into the plugin, so a copy is
// made for each player. It only changes the answer for the player it was
// made for, so that other people on the same console are left alone.

#include <wups.h>

#include <nn/act.h>

#include <cstdint>
#include <cstring>

#ifndef SMM_HOST
#error "Build with SMM_HOST, SMM_PORT, SMM_PID and SMM_NEX_PASSWORD set, see the README"
#endif

WUPS_PLUGIN_NAME("SMM private server");
WUPS_PLUGIN_DESCRIPTION("Plays Super Mario Maker on a private server");
WUPS_PLUGIN_VERSION("v0.1.0");
WUPS_PLUGIN_AUTHOR("super-mario-maker contributors");
WUPS_PLUGIN_LICENSE("AGPLv3");

// What the account system hands a game. Laid out as the Cemu emulator
// lays it out, which is what games read
struct NexToken {
    char token[0x201];
    uint8_t padding201[3];
    char nexPassword[0x41];
    uint8_t padding245[3];
    char host[0x10];
    uint16_t port;
    uint16_t padding25A;
};

static_assert(sizeof(NexToken) == 0x25C, "NexToken has the wrong size");
static_assert(offsetof(NexToken, nexPassword) == 0x204, "nexPassword is in the wrong place");
static_assert(offsetof(NexToken, host) == 0x248, "host is in the wrong place");
static_assert(offsetof(NexToken, port) == 0x258, "port is in the wrong place");
static_assert(sizeof(SMM_HOST) <= sizeof(NexToken::host), "SMM_HOST is too long. It must be an IPv4 address");
static_assert(sizeof(SMM_NEX_PASSWORD) <= sizeof(NexToken::nexPassword), "SMM_NEX_PASSWORD is too long");

// The game server IDs of Super Mario Maker, one for each region
static bool isSuperMarioMakerServer(uint32_t serverId) {
    return serverId == 0x1018DB00 || serverId == 0x1018DC00 || serverId == 0x1018DD00;
}

static void copyText(char *to, size_t size, const char *from) {
    memset(to, 0, size);
    strncpy(to, from, size - 1);
}

DECL_FUNCTION(uint32_t, AcquireNexServiceToken, NexToken *token, uint32_t serverId) {
    uint32_t result = real_AcquireNexServiceToken(token, serverId);

    // * The console's own network still decides whether the player may
    // * play online at all. A failure is passed on as it is
    if (!isSuperMarioMakerServer(serverId) || (result & 0x80000000) != 0) {
        return result;
    }

    if (nn::act::GetPrincipalId() != SMM_PID) {
        return result;
    }

    copyText(token->host, sizeof(token->host), SMM_HOST);
    token->port = SMM_PORT;
    copyText(token->nexPassword, sizeof(token->nexPassword), SMM_NEX_PASSWORD);

    // * The token the console's network made is not passed on to another
    // * server. This server does not check it, but a login needs one
    copyText(token->token, sizeof(token->token), "smm-private-server");

    return result;
}

WUPS_MUST_REPLACE(AcquireNexServiceToken, WUPS_LOADER_LIBRARY_NN_ACT, AcquireNexServiceToken__Q2_2nn3actFP26ACTNexAuthenticationResultUi);
