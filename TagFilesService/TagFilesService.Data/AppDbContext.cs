using Microsoft.EntityFrameworkCore;
using TagFilesService.Model;

namespace TagFilesService.Data;

public sealed class AppDbContext : DbContext
{
    public AppDbContext(DbContextOptions<AppDbContext> options)
        : base(options)
    {
        Database.EnsureCreated();
    }

    public DbSet<Tag> Tags => Set<Tag>();

    public DbSet<FileMetadata> FilesMetadata => Set<FileMetadata>();

    protected override void OnModelCreating(ModelBuilder modelBuilder)
    {
        modelBuilder.Entity<FileMetadata>()
            .HasMany(e => e.Tags)
            .WithMany();
    }
}