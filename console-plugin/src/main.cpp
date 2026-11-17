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
// Where to send the game, and the password to log in there, are read from
// a file on the SD card for each player, named after the player's ID on
// the console's network:
//
//   sd:/wiiu/smm-private-server/<player ID>.txt
//
//   host=203.0.113.4
//   port=6004
//   password=...
//
// A player with no file plays Super Mario Maker as before, so other people
// on the same console are left alone.

#include <wups.h>

#include <nn/act.h>

#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <cstring>

WUPS_PLUGIN_NAME("SMM private server");
WUPS_PLUGIN_DESCRIPTION("Plays Super Mario Maker on a private server");
WUPS_PLUGIN_VERSION("v0.2.0");
WUPS_PLUGIN_AUTHOR("super-mario-maker contributors");
WUPS_PLUGIN_LICENSE("AGPLv3");

// Lets the plugin read files on the SD card
WUPS_USE_WUT_DEVOPTAB();

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

struct Settings {
    char host[sizeof(NexToken::host)];
    uint16_t port;
    char password[sizeof(NexToken::nexPassword)];
};

// The game server IDs of Super Mario Maker, one for each region
static bool isSuperMarioMakerServer(uint32_t serverId) {
    return serverId == 0x1018DB00 || serverId == 0x1018DC00 || serverId == 0x1018DD00;
}

// * Copies a value that has to fit, and refuses one that does not, so
// * that a value is never cut short into something else
static bool copyValue(char *to, size_t size, const char *from) {
    size_t length = strcspn(from, "\r\n");
    if (length == 0 || length >= size) {
        return false;
    }

    memset(to, 0, size);
    memcpy(to, from, length);

    return true;
}

// * Reads the player's settings. Returns false when there are none, or
// * they are not complete, in which case nothing is changed
static bool readSettings(uint32_t pid, Settings &settings) {
    char path[96];
    snprintf(path, sizeof(path), "fs:/vol/external01/wiiu/smm-private-server/%lu.txt", (unsigned long) pid);

    FILE *file = fopen(path, "r");
    if (file == nullptr) {
        return false;
    }

    memset(&settings, 0, sizeof(settings));

    bool hasHost = false;
    bool hasPassword = false;
    char line[160];

    while (fgets(line, sizeof(line), file) != nullptr) {
        if (strncmp(line, "host=", 5) == 0) {
            hasHost = copyValue(settings.host, sizeof(settings.host), line + 5);
        } else if (strncmp(line, "port=", 5) == 0) {
            long port = strtol(line + 5, nullptr, 10);
            settings.port = (port > 0 && port < 65536) ? (uint16_t) port : 0;
        } else if (strncmp(line, "password=", 9) == 0) {
            hasPassword = copyValue(settings.password, sizeof(settings.password), line + 9);
        }
    }

    fclose(file);

    return hasHost && hasPassword && settings.port != 0;
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

    Settings settings;
    if (!readSettings(nn::act::GetPrincipalId(), settings)) {
        return result;
    }

    copyText(token->host, sizeof(token->host), settings.host);
    token->port = settings.port;
    copyText(token->nexPassword, sizeof(token->nexPassword), settings.password);

    // * The token the console's network made is not passed on to another
    // * server. This server does not check it, but a login needs one
    copyText(token->token, sizeof(token->token), "smm-private-server");

    return result;
}

WUPS_MUST_REPLACE(AcquireNexServiceToken, WUPS_LOADER_LIBRARY_NN_ACT, AcquireNexServiceToken__Q2_2nn3actFP26ACTNexAuthenticationResultUi);
