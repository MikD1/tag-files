using Microsoft.EntityFrameworkCore;
using TagFilesService.Model;
using TagFilesService.Model.Processing;

namespace TagFilesService.Infrastructure;

public sealed class AppDbContext : DbContext
{
    public AppDbContext(DbContextOptions<AppDbContext> options)
        : base(options)
    {
        Database.EnsureCreated();
    }

    public DbSet<Tag> Tags => Set<Tag>();

    public DbSet<FileMetadata> FilesMetadata => Set<FileMetadata>();

    public DbSet<ProcessingFile> ProcessingFiles => Set<ProcessingFile>();

    protected override void OnModelCreating(ModelBuilder modelBuilder)
    {
        modelBuilder.Entity<FileMetadata>()
            .Property(e => e.Type).HasConversion<string>();
        /*modelBuilder.Entity<FileMetadata>()
            .Property(e => e.ThumbnailStatus).HasConversion<string>();*/
        modelBuilder.Entity<FileMetadata>()
            .HasMany(e => e.Tags).WithMany();
    }
}