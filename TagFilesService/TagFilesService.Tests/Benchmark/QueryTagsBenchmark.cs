// using System.Diagnostics;
// using Microsoft.EntityFrameworkCore;
// using TagFilesService.Data;
// using TagFilesService.Model;
//
// namespace TagFilesService.Tests.Benchmark;
//
// [TestClass]
// public class QueryTagsBenchmark
// {
//     // [TestMethod]
//     public async Task SeedData()
//     {
//         DbContextOptions<AppDbContext> options = new DbContextOptionsBuilder<AppDbContext>()
//             .UseSqlite("Data Source=../../../tag-files-seed.db")
//             .Options;
//         await using AppDbContext dbContext = new(options);
//         await SeedData(dbContext);
//     }
//
//     // [TestMethod]
//     public async Task QuerySingleTagBenchmark()
//     {
//         DbContextOptions<AppDbContext> options = new DbContextOptionsBuilder<AppDbContext>()
//             .UseSqlite("Data Source=../../../tag-files-seed.db")
//             .Options;
//         await using AppDbContext dbContext = new(options);
//         TagsRepository repository = new(dbContext);
//
//         Stopwatch watch = Stopwatch.StartNew();
//         List<FileMetadata> result = await repository.QueryTags("tag42");
//         watch.Stop();
//         long elapsedMs = watch.ElapsedMilliseconds;
//         Console.WriteLine($"Elapsed time: {elapsedMs} ms");
//
//         Assert.AreEqual(6490, result.Count);
//     }
//
//     // [TestMethod]
//     public async Task AttachTagBenchmark()
//     {
//         DbContextOptions<AppDbContext> options = new DbContextOptionsBuilder<AppDbContext>()
//             .UseSqlite("Data Source=../../../tag-files-seed.db")
//             .Options;
//         await using AppDbContext dbContext = new(options);
//         TagsRepository repository = new(dbContext);
//
//         Stopwatch watch = Stopwatch.StartNew();
//         Tag tag = await repository.CreateTag("tag1001");
//         await repository.AttachTag(42, tag.Id);
//         watch.Stop();
//         long elapsedMs = watch.ElapsedMilliseconds;
//         Console.WriteLine($"Elapsed time: {elapsedMs} ms");
//     }
//
//     // [TestMethod]
//     public async Task QuerySingleFileBenchmark()
//     {
//         DbContextOptions<AppDbContext> options = new DbContextOptionsBuilder<AppDbContext>()
//             .UseSqlite("Data Source=../../../tag-files-seed.db")
//             .Options;
//         await using AppDbContext dbContext = new(options);
//         TagsRepository repository = new(dbContext);
//
//         Stopwatch watch = Stopwatch.StartNew();
//         List<FileMetadata> result = await repository.QueryTags("tag1001");
//         watch.Stop();
//         long elapsedMs = watch.ElapsedMilliseconds;
//         Console.WriteLine($"Elapsed time: {elapsedMs} ms");
//
//         Assert.AreEqual(1, result.Count);
//         Assert.AreEqual(42u, result[0].Id);
//     }
//
//     private async Task SeedData(AppDbContext dbContext)
//     {
//         Random random = new();
//
//         List<Tag> tags = GenerateTags(100);
//         dbContext.Tags.AddRange(tags);
//         await dbContext.SaveChangesAsync();
//
//         List<FileMetadata> filesMetadata = [];
//         for (int i = 1; i <= 100000; i++)
//         {
//             string path = $"file{i}";
//             int tagsCount = random.Next(3, 11);
//             List<Tag> selectedTags = tags
//                 .OrderBy(_ => random.Next())
//                 .Take(tagsCount)
//                 .ToList();
//
//             FileMetadata fileMetadata = new(path, FileType.Image, null, selectedTags);
//             filesMetadata.Add(fileMetadata);
//         }
//
//         dbContext.FilesMetadata.AddRange(filesMetadata);
//         await dbContext.SaveChangesAsync();
//     }
//
//     private List<Tag> GenerateTags(int total)
//     {
//         List<Tag> tags = [];
//         for (int i = 1; i <= total; i++)
//         {
//             tags.Add(new($"tag{i}"));
//         }
//
//         return tags;
//     }
// }