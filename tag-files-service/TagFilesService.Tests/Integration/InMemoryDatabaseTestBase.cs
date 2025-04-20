using Microsoft.Data.Sqlite;
using Microsoft.EntityFrameworkCore;
using TagFilesService.Infrastructure;

namespace TagFilesService.Tests.Integration;

public abstract class InMemoryDatabaseTestBase
{
    [TestInitialize]
    public void Initialize()
    {
        _connection = new("Filename=:memory:");
        _connection.Open();
        DbContextOptions<AppDbContext> options = new DbContextOptionsBuilder<AppDbContext>()
            .UseSqlite(_connection)
            .Options;
        DbContext = new(options);
    }

    [TestCleanup]
    public void Cleanup()
    {
        DbContext.Dispose();
        _connection.Dispose();
    }

    protected AppDbContext DbContext { get; private set; } = null!;

    private SqliteConnection _connection = null!;
}